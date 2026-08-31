# GlassBox-Go: Usage Guide

## Installation

```bash
go get github.com/GusGtc/glassbox-go
```

## Minimal Server Integration

3 lines of code to fingerprint every visitor:

```go
package main

import (
	"log"
	"net/http"

	"github.com/GusGtc/glassbox-go/glassbox"
)

func main() {
	gb, err := glassbox.New(glassbox.Options{DBPath: "glassbox.db"})
	if err != nil {
		log.Fatal(err)
	}
	defer gb.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body><h1>Hello</h1></body></html>"))
	})

	// 1) Mount WebSocket + JS endpoint
	gb.Mount(mux)

	// 2) Wrap handler to inject probe <script> into every HTML response
	log.Fatal(http.ListenAndServe(":8080", gb.Wrap(mux)))
}
```

Every HTML response is transparently rewritten to include the fingerprinting
script. The script runs client-side, collects signals, and POSTs them back over
WebSocket. The server stores them in SQLite.

## Configuration Options

```go
gb, err := glassbox.New(glassbox.Options{
	DBPath:         "glassbox.db",       // SQLite path (required)
	RecordIP:       true,                // store client IP (default false)
	GeoLookup:      true,                // probe 26 — IP geolocation via ipapi
	AllowAnyOrigin: true,                // skip WebSocket Origin check
	Log:            myLogger,            // *log.Logger (nil = discard)
})
```

## How the Probe Works

1. Client requests `/` → server returns HTML with injected `<script>`
2. Script connects to WebSocket at `/glassbox/ws`
3. Server sends probe tasks over WS (canvas, WebGL, audio, fonts, etc.)
4. Client executes each probe and sends results back as JSON
5. Server computes a 40-d characteristic vector, deduplicates into a BrowserID,
   and stores the visit + signals in SQLite

## Querying Similar Browsers

### From Go code

```go
store := gb.Store()

// Find browsers similar to a given BrowserID
hits, err := store.NearestBrowser(ctx, browserID, 10, 0, 0)
// hits[0].Match.Similarity  — cosine score [0,1]
// hits[0].Match.Distance    — weighted Euclidean (lower = closer)
// hits[0].ID                — BrowserID hex string
// hits[0].UA, .Bits, .Band  — metadata

// Find similar visits (per-request, not deduplicated)
targetVec := glassbox.ExtractVector(signals)
hits, err := store.Nearest(ctx, targetVec, 10, 0, 0)

// Get all visits for a browser
visits, err := store.VisitsForBrowser(ctx, browserID)

// Get all signals for a browser
sigs, err := store.SignalsForBrowser(ctx, browserID)
```

### From the CLI (`gbquery`)

```bash
gbquery -db glassbox.db browsers                    # list all deduplicated browsers
gbquery -db glassbox.db browser -id <HEX>           # show one browser's vector
gbquery -db glassbox.db similar -browser <HEX> -k 10  # top-10 similar browsers
gbquery -db glassbox.db show -id 7                  # show visit #7's signals
gbquery -db glassbox.db nearest -id 7 -k 5          # top-5 similar visits
gbquery -db glassbox.db find -ua "HeadlessChrome"   # search by UA substring
gbquery -db glassbox.db seed -n 6                   # insert 6 synthetic test browsers
gbquery -db glassbox.db schema                      # dump DB schema
```

## Integrating with Existing Middleware

If you already have middleware (auth, logging, etc.), chain them:

```go
mux := http.NewServeMux()
// ... your routes ...

gb.Mount(mux)

// GlassBox outermost (captures all HTML responses)
handler := gb.Wrap(mux)

// Your middleware wraps around GlassBox
handler = myAuthMiddleware(handler)
handler = myLoggingMiddleware(handler)

http.ListenAndServe(":8080", handler)
```

Order matters: `Wrap` must see the HTML response before your other middleware
modifies it. Put `gb.Wrap()` as the innermost wrapper (closest to the mux).

## Serving API + HTML Pages Together

`Mount` registers `/glassbox/ws` and `/glassbox/glassbox.js` on the mux.
`Wrap` only rewrites responses with `Content-Type: text/html`. JSON endpoints
are untouched.

```go
mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
})

mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    w.Write([]byte("<html><body>Page with fingerprinting</body></html>"))
})

gb.Mount(mux)
log.Fatal(http.ListenAndServe(":8080", gb.Wrap(mux)))
```

`/api/data` returns JSON → no script injected.
`/page` returns HTML → probe script auto-injected.

## Using Without HTTP (Programmatic)

If you already have signals from another source (e.g. a JS SDK posting to a
REST endpoint), skip the WebSocket flow and compute vectors directly:

```go
signals := []glassbox.Signal{
    {Tier: "hw", Key: "cores", Value: "8"},
    {Tier: "hw", Key: "mem", Value: "16"},
    {Tier: "engine", Key: "canvas", Value: "abc123hash"},
    // ...
}

vec := glassbox.ExtractVector(signals)
id := glassbox.BrowserID(vec)  // 16-char hex

v := glassbox.Visit{
    UA:   "Mozilla/5.0 ...",
    Bits: 28,
    Band: "Highly identifying",
    Pct:  80,
    Vec:  vec,
}
visitID, _ := store.Save(ctx, v, signals)
```

## ZCA Whitening (Automatic)

A background goroutine periodically samples stored vectors and learns a
ZCA whitening transform. Once enough samples accumulate (>100), vectors are
whitened at write time. This decorrelates the 40 dimensions and improves
similarity discrimination.

No configuration needed — it runs automatically after server start.

To check if whitening is active:

```go
zca := glassbox.GetZCA()
if zca.Wzca != nil {
    fmt.Println("ZCA whitening is active")
}
```

## Database

SQLite by default. Schema has three tables:

- **visits** — one row per HTTP request that loaded the probe
- **browsers** — deduplicated by `BrowserID(vec)`, merged on each new visit
- **signals** — raw `(tier, key, value)` tuples per visit

Use `store.DB()` to access the raw `*sql.DB` for custom queries.

## Serving Static Files with Fingerprinting

```go
mux.Handle("/", http.FileServer(http.Dir("./static")))
gb.Mount(mux)
http.ListenAndServe(":8080", gb.Wrap(mux))
```

Any `.html` file in `./static` automatically gets the probe script injected.
CSS, JS, images, and JSON files are served as-is.

## Error Handling

`glassbox.New()` returns an error if:
- SQLite cannot be opened
- The schema migration fails

`store.Save()` returns an error if the DB write fails. Probes that fail
client-side (WebGL unsupported, etc.) return zero values — they don't crash
the page.

## Running the Demo Server

```bash
cd github.com/GusGtc/glassbox-go
go run ./cmd/glassbox-server -addr :8080 -dir ./example -db glassbox.db
```

Open `http://localhost:8080` in a browser. The probe runs automatically.
Visit logs appear on stdout.

## Running the Test Suite (Playwright)

```bash
# Start the server
./glassbox-server -addr :9999 -dir ./example -db test.db &

# Run Playwright (requires npm install playwright)
node test_glassbox.js

# Check similarity
./gbquery -db test.db nearest -id 1 -k 3
```

Two visits from the same browser context should show `SIM=1.000, DIST=0.0000`.
