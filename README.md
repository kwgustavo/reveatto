# GlassBox-Go

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

Drop-in Go middleware that fingerprints every visitor to a Go web application,
deduplicates them into stable **BrowserIDs**, and lets you find similar browsers
using a weighted combination of (a) 40-d Euclidean distance on a numeric
characteristic vector, and (b) entropy-weighted Jaccard similarity over the
original `(tier, key, value)` signals.

Browser-side probes are ported from the upstream GlassBox project. On every
HTML response the library injects a `<script>` that runs the probes and posts
a single JSON message back over WebSocket.

## Quick Start

```go
package main

import (
    "log"
    "net/http"
	"github.com/GusGtc/glassbox-go/glassbox"
)

func main() {
    gb, _ := glassbox.New(glassbox.Options{DBPath: "visits.db"})
    defer gb.Close()

    mux := http.NewServeMux()
    mux.Handle("/", http.FileServer(http.Dir("./static")))

    gb.Mount(mux)
    log.Fatal(http.ListenAndServe(":8080", gb.Wrap(mux)))
}
```

Every HTML response served through `handler` is rewritten to include the probe
script. Each visitor becomes one row in `visits`, deduplicated into one row in
`browsers` keyed by `BrowserID(vec)`. Use `Store.NearestBrowser(ctx, id, k)`
to retrieve the top-k similar browsers.

## How similarity is computed

Two independent scores are computed and the ranking uses both:

### 1. Distance — Weighted Euclidean on a 40-d characteristic vector

The vector has 40 features, each normalized to `[0,1]`. High-entropy
categoricals (canvas, audio, codecs, math) are split into 4 hash bytes each,
so two browsers with similar values get similar vectors. Per-feature weights
boost discriminative signals:

| idx | tier | key | source |
|-----|------|-----|--------|
| 0 | hw | cores | `hw.cores` |
| 1 | hw | mem_gb | `hw.mem` |
| 2 | hw | screen_w | `hw.screen` |
| 3 | hw | screen_h | `hw.screen` |
| 4 | hw | screen_depth | `hw.screen` |
| 5 | hw | hz | `hw.hz` |
| 6 | hw | tz_hours | `hw.tz` (IANA → UTC offset) |
| 7 | hw | lang_count | `len(hw.langs)` |
| 8 | hw | touch | `hw.touch` |
| 9 | hw | dst | `hw.dst` |
| 10..13 | engine | canvas (4 bytes) | FNV-64(`engine.canvas`) |
| 14..17 | engine | glext (4 bytes) | FNV-64(`engine.glext`) |
| 18..21 | engine | audio (4 bytes) | FNV-64(`engine.audio`) |
| 22..25 | engine | fonts (4 bytes) | FNV-64(`engine.fonts`) |
| 26..29 | engine | codecs (4 bytes) | FNV-64(`engine.codecs`) |
| 30..33 | engine | math (4 bytes) | FNV-64(`engine.math`) |
| 34..37 | build | uach (4 bytes) | FNV-64(`build.uach \|\| build.ua`) |
| 38 | build | kbd | FNV-64(`build.kbd`) |
| 39 | session | devices_n | `len(session.devices)/16` |

Weights (default `FeatureWeights` in `featurevec.go`):

```go
var FeatureWeights = [VecDim]float32{
    1.5, 1.5, 2.0, 2.0, 1.0, 1.5, 1.5, 0.5, 0.1, 0.1,    // hw
    2.5, 2.5, 2.5, 2.5,                                  // canvas
    2.0, 2.0, 2.0, 2.0,                                  // glext
    2.5, 2.5, 2.5, 2.5,                                  // audio
    2.0, 2.0, 2.0, 2.0,                                  // fonts
    2.5, 2.5, 2.5, 2.5,                                  // codecs
    2.5, 2.5, 2.5, 2.5,                                  // math
    1.0, 1.0, 1.0, 1.0,                                  // uach
    2.0,                                                  // kbd
    1.0,                                                  // devices
}
```

`Distance = sqrt(Σ w_i · (a_i - b_i)^2)`. Smaller = closer.

### 2. Similarity — Entropy-weighted Jaccard over signal values

For each `(tier, key)` we ask "do both signals have the same value?".
For continuous signals we allow a small tolerance (e.g. cores ±0, mem ±1 GB,
screen resolution match if w/h match, refresh rate ±5 Hz). Then we compute:

```
Similarity = Σ w_match / Σ w_union
```

where `w` is the entropy contribution of each `(tier, key)` (from the index.html
model: canvas=6, audio=5, codecs=5, math=4, fonts=4, kbd=5, etc.).

This is **directly interpretable**: 0.371 means "37% of the entropy of the
union of these two browsers comes from signals they share". If two browsers
share canvas, audio, codecs, and timezone but differ in uach brand, they score
high.

### Ranking

`Store.NearestBrowser` returns matches sorted by **`Similarity DESC`, then
`Distance ASC`**. Both are exposed in `MatchedBrowser.Match.Distance` and
`MatchedBrowser.Match.Similarity`.

### Thresholds (rough)

- `Similarity > 0.30` & `Distance < 4.0` — almost certainly same browser family
- `Similarity 0.10..0.30` — same platform or same person, different browser
- `Similarity < 0.10` — unrelated

## API

```go
// Compute vector + BrowserID from raw signals.
vec := glassbox.ExtractVector(payload.Signals())
id  := glassbox.BrowserID(vec)

// Find similar browsers.
store := gb.Store()
hits, _ := store.NearestBrowser(ctx, id, 10, 0, 0)
for _, h := range hits {
    fmt.Printf("sim=%.3f dist=%.4f ua=%s\n",
        h.Match.Similarity, h.Match.Distance, h.UA)
}
```

You can also pass a raw vector and find similar visits (one row per HTTP
request, not per deduplicated browser):

```go
hits, _ := store.Nearest(ctx, targetVec, 10, 0, 0)
```

## Database schema

```sql
CREATE TABLE visits (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    browser_id    TEXT,
    session_id    TEXT NOT NULL,
    first_seen    INTEGER NOT NULL,
    last_seen     INTEGER NOT NULL,
    ua            TEXT,
    ip            TEXT,
    country       TEXT,
    pct           REAL,
    bits          REAL,
    band          TEXT,
    rfp_detected  INTEGER,
    canvas_masked INTEGER,
    gl_masked     INTEGER,
    tz_spoofed    INTEGER,
    vec           BLOB,    -- 160 bytes: 40 * float32 little-endian
    raw           TEXT
);

CREATE TABLE browsers (
    id          TEXT PRIMARY KEY,        -- BrowserID(vec)
    first_seen  INTEGER NOT NULL,
    last_seen   INTEGER NOT NULL,
    visit_count INTEGER NOT NULL DEFAULT 1,
    ua          TEXT,
    pct         REAL,
    bits        REAL,
    band        TEXT,
    vec         BLOB
);

CREATE TABLE signals (
    visit_id INTEGER NOT NULL REFERENCES visits(id) ON DELETE CASCADE,
    tier     TEXT NOT NULL,
    key      TEXT NOT NULL,
    value    TEXT NOT NULL,
    PRIMARY KEY (visit_id, key)
);
```

## CLI: `gbquery`

```bash
gbquery -db visits.db seed -n 6                # add 6 distinct synthetic browsers
gbquery -db visits.db browsers                  # list deduplicated browsers
gbquery -db visits.db browser -id HEX           # show one browser
gbquery -db visits.db similar -browser HEX -k 10  # top-10 similar
gbquery -db visits.db show -id 7                # show visit #7
gbquery -db visits.db nearest -id 7 -k 5        # top-5 similar visits (per request)
gbquery -db visits.db find -ua "HeadlessChrome" # search by UA substring
gbquery -db visits.db schema
```

## Example server

`example_server/` ships a small app that:
- injects the probe on every HTML response
- serves `/`, `/visits.json`, `/browsers`, `/browsers.json`
- serves `/browser/{id}` and `/browser/{id}/similar` (HTML + JSON)
- ships a `/profiles?profile=desktop_chrome` page that lets you drive the
  fingerprint with synthetic data, so you can verify similarity ranking
  without opening multiple real browsers
- ships a `/seed` page that inserts 6 hand-crafted distinct profiles into
  the database for testing

## License

This project is licensed under the GNU General Public License v3.0 — see the [LICENSE](LICENSE) file for details.
