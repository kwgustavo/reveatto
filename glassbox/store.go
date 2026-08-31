package glassbox

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Visit is a single observation from the browser probes.
type Visit struct {
	SessionID string
	FirstSeen time.Time
	LastSeen  time.Time
	UA        string
	IP        string
	Country   string
	Pct       float64
	Bits      float64
	Band      string
	Masked    Masked
	Vec       []float32
	Raw       string
}

// Match is a similarity score pair between two browsers.
type Match struct {
	Distance   float64 // weighted Euclidean (smaller = closer)
	Similarity float64 // TierSimilarity 0..1 (larger = more alike)
}

// MatchedVisit is one row returned from Nearest.
type MatchedVisit struct {
	ID        int64
	SessionID string
	UA        string
	Bits      float64
	Band      string
	Match     Match
}

// Browser is a deduplicated browser (keyed by BrowserID(vec)).
type Browser struct {
	ID         string
	FirstSeen  time.Time
	LastSeen   time.Time
	VisitCount int
	UA         string
	Pct        float64
	Bits       float64
	Band       string
	Vec        []float32
	Signals    []Signal
}

// MatchedBrowser is one row returned from NearestBrowser.
type MatchedBrowser struct {
	ID         string
	UA         string
	Pct        float64
	Bits       float64
	Band       string
	VisitCount int
	Match      Match
}

// Masked mirrors the masking flags re-derived server-side.
type Masked struct {
	RFP         bool
	CanvasRand  bool
	CanvasSpoof bool
	GLMasked    bool
	TZSpoofed   bool
}

// Store is a thin SQLite-backed persistence layer.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

// Open creates (or opens) a SQLite database at path and runs the schema.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("glassbox: db path is empty")
	}
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB exposes the underlying database (for advanced queries).
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	_, err := s.db.ExecContext(context.Background(), schemaSQL)
	if err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
}

// Save atomically inserts the visit row plus its (tier, key, value) signals.
// Returns the visit ID. If v.Vec is non-empty, the visit is also upserted
// into the browsers table, keyed by BrowserID(v.Vec).
func (s *Store) Save(ctx context.Context, v Visit, sigs []Signal) (int64, error) {
	if v.FirstSeen.IsZero() {
		v.FirstSeen = time.Now()
	}
	if v.LastSeen.IsZero() {
		v.LastSeen = v.FirstSeen
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var vecBytes any
	if len(v.Vec) > 0 {
		vecBytes = PackVector(v.Vec)
	}
	var bid any
	if len(v.Vec) > 0 {
		bid = BrowserID(v.Vec)
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO visits
		  (browser_id, session_id, first_seen, last_seen, ua, ip, country, pct, bits, band,
		   rfp_detected, canvas_masked, gl_masked, tz_spoofed, vec, raw)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		bid,
		v.SessionID,
		v.FirstSeen.UnixMilli(), v.LastSeen.UnixMilli(),
		nullable(v.UA), nullable(v.IP), nullable(v.Country),
		v.Pct, v.Bits, v.Band,
		b2i(v.Masked.RFP), b2i(v.Masked.CanvasSpoof||v.Masked.CanvasRand),
		b2i(v.Masked.GLMasked), b2i(v.Masked.TZSpoofed),
		vecBytes,
		nullable(v.Raw),
	)
	if err != nil {
		return 0, fmt.Errorf("insert visit: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if len(sigs) > 0 {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO signals (visit_id, tier, key, value) VALUES (?,?,?,?)`)
		if err != nil {
			return 0, err
		}
		defer stmt.Close()
		for _, sg := range sigs {
			if sg.Key == "" || sg.Value == "" {
				continue
			}
			if _, err := stmt.ExecContext(ctx, id, sg.Tier, sg.Key, sg.Value); err != nil {
				return 0, fmt.Errorf("insert signal %s: %w", sg.Key, err)
			}
		}
	}
	if len(v.Vec) > 0 {
		bid := BrowserID(v.Vec)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO browsers (id, first_seen, last_seen, visit_count, ua, pct, bits, band, vec)
			VALUES (?,?,?,1,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
			  last_seen  = excluded.last_seen,
			  visit_count = browsers.visit_count + 1,
			  ua         = excluded.ua,
			  pct        = excluded.pct,
			  bits       = excluded.bits,
			  band       = excluded.band,
			  vec        = excluded.vec
		`, bid, v.FirstSeen.UnixMilli(), v.LastSeen.UnixMilli(),
			nullable(v.UA), v.Pct, v.Bits, v.Band, vecBytes)
		if err != nil {
			return 0, fmt.Errorf("upsert browser: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return id, nil
}

// Signal is a single characteristic-vector entry: (tier, key, value).
type Signal struct {
	Tier  string
	Key   string
	Value string
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SignalsForBrowser returns the signals from the most recent visit of the
// given browser (by browser_id). Used to compute TierSimilarity.
func (s *Store) SignalsForBrowser(ctx context.Context, browserID string) ([]Signal, error) {
	var visitID int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM visits WHERE browser_id = ? ORDER BY id DESC LIMIT 1`, browserID).
		Scan(&visitID)
	if err != nil {
		return nil, err
	}
	return s.SignalsForVisit(ctx, visitID)
}

// SignalsForVisit returns all signals recorded for a visit.
func (s *Store) SignalsForVisit(ctx context.Context, visitID int64) ([]Signal, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT tier, key, value FROM signals WHERE visit_id = ? ORDER BY tier, key`, visitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Signal
	for rows.Next() {
		var sig Signal
		if err := rows.Scan(&sig.Tier, &sig.Key, &sig.Value); err != nil {
			return nil, err
		}
		out = append(out, sig)
	}
	return out, rows.Err()
}

// Nearest finds up to limit visits whose characteristic vector is closest
// (weighted Euclidean) to target.
func (s *Store) Nearest(ctx context.Context, target []float32, limit int, minBits, minPct float64) ([]MatchedVisit, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, ua, bits, band, vec
		FROM visits
		WHERE vec IS NOT NULL
		  AND (? = 0 OR bits >= ?)
		  AND (? = 0 OR pct >= ?)
		ORDER BY id DESC
		LIMIT 5000`, minBits, minBits, minPct, minPct)
	if err != nil {
		return nil, fmt.Errorf("nearest query: %w", err)
	}
	defer rows.Close()

	type cv struct {
		m  MatchedVisit
		v  []float32
		id int64
	}
	var cands []cv
	for rows.Next() {
		var m MatchedVisit
		var vecBlob []byte
		if err := rows.Scan(&m.ID, &m.SessionID, &m.UA, &m.Bits, &m.Band, &vecBlob); err != nil {
			return nil, err
		}
		if len(vecBlob) == 0 {
			continue
		}
		cands = append(cands, cv{m: m, v: UnpackVector(vecBlob), id: m.ID})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// load source signals if any of the candidates need TierSimilarity; for
	// visits we don't have the source signals here, so just use distance.
	for i := range cands {
		cands[i].m.Match.Distance = WeightedVecEuclidean(target, cands[i].v)
		cands[i].m.Match.Similarity = WeightedVecCosine(target, cands[i].v)
	}
	out := make([]MatchedVisit, 0, limit)
	for i := 0; i < len(cands) && i < limit*5; i++ {
		inserted := false
		for j := 0; j < len(out); j++ {
			if cands[i].m.Match.Distance < out[j].Match.Distance {
				out = append(out[:j], append([]MatchedVisit{cands[i].m}, out[j:]...)...)
				inserted = true
				break
			}
		}
		if !inserted {
			out = append(out, cands[i].m)
		}
		if len(out) > limit {
			out = out[:limit]
		}
	}
	return out, nil
}

// Browser returns the browser row for id (or sql.ErrNoRows).
func (s *Store) Browser(ctx context.Context, id string) (*Browser, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, first_seen, last_seen, visit_count, ua, pct, bits, band, vec
		FROM browsers WHERE id = ?`, id)
	var b Browser
	var first, last int64
	var vecBlob []byte
	var ua *string
	if err := row.Scan(&b.ID, &first, &last, &b.VisitCount, &ua, &b.Pct, &b.Bits, &b.Band, &vecBlob); err != nil {
		return nil, err
	}
	b.FirstSeen = time.UnixMilli(first)
	b.LastSeen = time.UnixMilli(last)
	if ua != nil {
		b.UA = *ua
	}
	if len(vecBlob) > 0 {
		b.Vec = UnpackVector(vecBlob)
	}
	sigs, err := s.SignalsForBrowser(ctx, id)
	if err == nil {
		b.Signals = sigs
	}
	return &b, nil
}

// Browsers lists up to limit browsers ordered by last_seen DESC.
func (s *Store) Browsers(ctx context.Context, limit int) ([]Browser, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, first_seen, last_seen, visit_count, ua, pct, bits, band, vec
		FROM browsers ORDER BY last_seen DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Browser
	for rows.Next() {
		var b Browser
		var first, last int64
		var vecBlob []byte
		var ua *string
		if err := rows.Scan(&b.ID, &first, &last, &b.VisitCount, &ua, &b.Pct, &b.Bits, &b.Band, &vecBlob); err != nil {
			return nil, err
		}
		b.FirstSeen = time.UnixMilli(first)
		b.LastSeen = time.UnixMilli(last)
		if ua != nil {
			b.UA = *ua
		}
		if len(vecBlob) > 0 {
			b.Vec = UnpackVector(vecBlob)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// NearestBrowser returns the top-k OTHER browsers ranked by similarity.
// Both metrics are computed:
//   - Distance:   weighted Euclidean on the 40-d vector (smaller = closer)
//   - Similarity: weighted Jaccard over signal values (larger = more alike)
//
// Results are ordered by Similarity DESC, then Distance ASC.
//
// minBits/minPct optionally filter by bits/pct (use 0 to disable).
func (s *Store) NearestBrowser(ctx context.Context, browserID string, k int, minBits, minPct float64) ([]MatchedBrowser, error) {
	if k <= 0 {
		k = 10
	}
	src, err := s.Browser(ctx, browserID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, ua, pct, bits, band, visit_count, vec
		FROM browsers
		WHERE vec IS NOT NULL AND id <> ?
		  AND (? = 0 OR bits >= ?)
		  AND (? = 0 OR pct >= ?)
		ORDER BY last_seen DESC LIMIT 5000`,
		browserID, minBits, minBits, minPct, minPct)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type cand struct {
		m MatchedBrowser
		v []float32
	}
	var cands []cand
	var ids []string
	for rows.Next() {
		var mb MatchedBrowser
		var vecBlob []byte
		if err := rows.Scan(&mb.ID, &mb.UA, &mb.Pct, &mb.Bits, &mb.Band, &mb.VisitCount, &vecBlob); err != nil {
			return nil, err
		}
		if len(vecBlob) == 0 {
			continue
		}
		cands = append(cands, cand{m: mb, v: UnpackVector(vecBlob)})
		ids = append(ids, mb.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// load signals for each candidate
	sigs := make([][]Signal, len(cands))
	for i, id := range ids {
		sg, err := s.SignalsForBrowser(ctx, id)
		if err == nil {
			sigs[i] = sg
		}
	}
	for i := range cands {
		cands[i].m.Match.Distance = WeightedVecEuclidean(src.Vec, cands[i].v)
		cands[i].m.Match.Similarity = WeightedVecCosine(src.Vec, cands[i].v)
	}
	// sort: Similarity DESC, then Distance ASC
	out := make([]MatchedBrowser, 0, k)
	for i := 0; i < len(cands) && i < k*10; i++ {
		inserted := false
		for j := 0; j < len(out); j++ {
			if better(cands[i].m.Match, out[j].Match) {
				out = append(out[:j], append([]MatchedBrowser{cands[i].m}, out[j:]...)...)
				inserted = true
				break
			}
		}
		if !inserted {
			out = append(out, cands[i].m)
		}
		if len(out) > k {
			out = out[:k]
		}
	}
	return out, nil
}

// better returns true if a should be ranked before b.
// Higher Similarity wins; ties broken by smaller Distance.
func better(a, b Match) bool {
	if a.Similarity != b.Similarity {
		return a.Similarity > b.Similarity
	}
	return a.Distance < b.Distance
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
