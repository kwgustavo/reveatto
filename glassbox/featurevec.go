package glassbox

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash/fnv"
	"math"
	"math/rand"
	"strconv"
	"strings"
)

// LSHVector generates a Locality-Sensitive Hash vector for categorical data.
// It maps a comma-separated string of tokens to K continuous floats in [0,1]
// using random projection with deterministic seeding per token.
func LSHVector(rawData string, K int) []float32 {
	vec := make([]float32, K)
	if rawData == "" {
		return vec // all zeros
	}
	tokens := strings.Split(rawData, ",")
	for _, tok := range tokens {
		// trim spaces and skip empty tokens
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		// create deterministic seed from the token
		h := sha256.Sum256([]byte(tok))
		seed := int64(binary.BigEndian.Uint64(h[:8]))
		// initialize a local PRNG
		r := rand.New(rand.NewSource(seed))
		// accumulate normally distributed values
		for i := 0; i < K; i++ {
			vec[i] += float32(r.NormFloat64())
		}
	}
	// squash to [0,1] with a sigmoid (preserves ordering)
	for i := range vec {
		vec[i] = 1.0 / (1.0 + float32(math.Exp(-float64(vec[i]))))
	}
	return vec
}

// writeLSH writes the LSH result into the output vector at the given offset.
// Assumes 4 slots per engine hash/probe.
func writeLSH(out []float32, offset int, raw string) {
	v := LSHVector(raw, 4) // we always have 4 slots per engine hash
	copy(out[offset:offset+4], v)
}

// VecDim is the dimensionality of the characteristic vector.
// Every feature is normalized to [0,1] so distances are comparable.
const VecDim = 40

// Normalizer[i] is the divisor applied to feature i before storage.
var Normalizer = [VecDim]float32{
	// hw tier (10)
	64,    // 0  hw.cores
	128,   // 1  hw.mem
	7680,  // 2  hw.screen_w
	4320,  // 3  hw.screen_h
	48,    // 4  hw.screen_depth
	240,   // 5  hw.hz
	24,    // 6  hw.tz_offset_hours (range: -12..+12)
	8,     // 7  hw.lang_count
	1,     // 8  hw.touch (bool)
	1,     // 9  hw.dst (bool)

	// engine tier (24) — 6 features × 4 hash bytes each
	1, 1, 1, 1, // 10..13 engine.canvas
	1, 1, 1, 1, // 14..17 engine.glext
	1, 1, 1, 1, // 18..21 engine.audio
	1, 1, 1, 1, // 22..25 engine.fonts
	1, 1, 1, 1, // 26..29 engine.codecs
	1, 1, 1, 1, // 30..33 engine.math

	// build tier (5)
	1, 1, 1, 1, // 34..37 build.uach_brand_count / model / arch bits
	1, // 38 build.kbd_hash

	// session tier (1)
	1, // 39 session.devices_n (already in [0,1] after /16)
}

// Normalizer is fine-tuned for /255 on byte-split features. For the
// per-feature normalizer of non-byte features, override below.
var _ = strings.Split // keep import

// FeatureWeights[i] = relative importance of feature i in Euclidean distance.
// High-entropy categoricals (canvas, audio, codecs) and screen get the most
// weight; touch/dst get almost none because they're ~constant across users.
var FeatureWeights = [VecDim]float32{
	// hw
	1.5, 1.5, 2.0, 2.0, 1.0, 1.5, 1.5, 0.5, 0.1, 0.1,
	// engine
	2.5, 2.5, 2.5, 2.5, // canvas
	2.0, 2.0, 2.0, 2.0, // glext
	2.5, 2.5, 2.5, 2.5, // audio
	2.0, 2.0, 2.0, 2.0, // fonts
	2.5, 2.5, 2.5, 2.5, // codecs
	2.5, 2.5, 2.5, 2.5, // math
	// build
	1.0, 1.0, 1.0, 1.0, // uach / brand hash
	2.0, // kbd
	// session
	1.0, // devices_n
}

// FeatureNames lists human-readable names for debugging / display.
var FeatureNames = [VecDim]string{
	"cores", "mem_gb", "screen_w", "screen_h", "screen_depth", "hz",
	"tz_hours", "lang_count", "touch", "dst",
	"canvas_b0", "canvas_b1", "canvas_b2", "canvas_b3",
	"glext_b0", "glext_b1", "glext_b2", "glext_b3",
	"audio_b0", "audio_b1", "audio_b2", "audio_b3",
	"fonts_b0", "fonts_b1", "fonts_b2", "fonts_b3",
	"codecs_b0", "codecs_b1", "codecs_b2", "codecs_b3",
	"math_b0", "math_b1", "math_b2", "math_b3",
	"uach_b0", "uach_b1", "uach_b2", "uach_b3",
	"kbd",
	"devices_n",
}

// ExtractVector turns a list of (tier, key, value) signals into a normalized
// VecDim-dimensional float32 vector. Missing values are 0.
func ExtractVector(sigs []Signal) []float32 {
	m := make(map[string]string, len(sigs))
	for _, s := range sigs {
		m[s.Tier+"."+s.Key] = s.Value
	}
	out := make([]float32, VecDim)

	// hw
	out[0] = parseF(m["hw.cores"])
	out[1] = parseF(m["hw.mem"])
	w, h, d := parseScreen(m["hw.screen"])
	out[2] = w
	out[3] = h
	out[4] = d
	out[5] = parseF(m["hw.hz"])
	out[6] = tzHours(m["hw.tz"])
	out[7] = float32(parseCount(m["hw.langs"]))
	out[8] = boolF(m["hw.touch"])
	out[9] = boolF(m["hw.dst"])

	// engine — 4 bytes each
	writeLSH(out, 10, m["engine.canvas"]) // 10..13
	writeLSH(out, 14, m["engine.glext"])  // 14..17
	writeLSH(out, 18, m["engine.audio"])  // 18..21
	writeLSH(out, 22, m["engine.fonts"])  // 22..25
	writeLSH(out, 26, m["engine.codecs"]) // 26..29
	writeLSH(out, 30, m["engine.math"])   // 30..33

	// build — uach (parsed JSON-ish) + kbd
	uachPlus := m["build.uach"] + "||" + m["build.ua"]
	writeLSH(out, 34, uachPlus) // 34..37
	// kbd: 1 slot left, use LSH with K=1
	if v := LSHVector(m["build.kbd"], 1); len(v) > 0 {
		out[38] = v[0]
	}

	// session
	out[39] = float32(parseCount(m["session.devices"]))
	out[39] = out[39] / 16
	if out[39] > 1 {
		out[39] = 1
	}

	for i := range out {
		if Normalizer[i] > 0 {
			out[i] /= Normalizer[i]
		}
		if out[i] < 0 {
			out[i] = 0
		}
		if out[i] > 1 {
			out[i] = 1
		}
	}
	// ---- APPLY ZCA WHITENING ------------------------------------------------
	if wh := GetZCA(); wh.Wzca != nil {
		out = wh.Transform(out)
	}
	// -------------------------------------------------------------------------

	return out
}

// PackVector serializes []float32 to little-endian bytes.
func PackVector(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(x))
	}
	return b
}

// UnpackVector reverses PackVector.
func UnpackVector(b []byte) []float32 {
	n := len(b) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}

// Euclidean returns the unweighted Euclidean distance between two equal-length
// vectors. Provided for backwards compatibility; prefer WeightedEuclidean.
func Euclidean(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var sum float64
	for i := 0; i < n; i++ {
		d := float64(a[i] - b[i])
		sum += d * d
	}
	for i := n; i < len(a); i++ {
		d := float64(a[i])
		sum += d * d
	}
	return math.Sqrt(sum)
}

// WeightedEuclidean returns a weighted Euclidean distance: sqrt(Σ w_i * (a_i-b_i)^2).
// Lower = more similar.
func WeightedEuclidean(a, b []float32, weights []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var sum float64
	for i := 0; i < n; i++ {
		d := float64(a[i] - b[i])
		w := 1.0
		if i < len(weights) {
			w = float64(weights[i])
		}
		sum += w * d * d
	}
	return math.Sqrt(sum)
}

// WeightedVecEuclidean is a convenience wrapper using FeatureWeights.
func WeightedVecEuclidean(a, b []float32) float64 {
	w := make([]float32, VecDim)
	copy(w, FeatureWeights[:])
	return WeightedEuclidean(a, b, w)
}

// WeightedCosine returns the weighted cosine similarity between two vectors:
// sum(w_i * a_i * b_i) / sqrt(sum(w_i * a_i^2) * sum(w_i * b_i^2)).
// Returns 0 if either denominator is zero.
func WeightedCosine(a, b []float32, weights []float32) float64 {
	var num, denA, denB float64
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		fa := float64(a[i])
		fb := float64(b[i])
		w := 1.0
		if i < len(weights) {
			w = float64(weights[i])
		}
		num += w * fa * fb
		denA += w * fa * fa
		denB += w * fb * fb
	}
	if denA == 0 || denB == 0 {
		return 0
	}
	return num / (math.Sqrt(denA) * math.Sqrt(denB))
}

// WeightedVecCosine is a convenience wrapper using FeatureWeights.
func WeightedVecCosine(a, b []float32) float64 {
	w := make([]float32, VecDim)
	copy(w, FeatureWeights[:])
	return WeightedCosine(a, b, w)
}

// BrowserID returns a stable identifier for a browser from its vec.
// The vector bytes are FNV-64 hashed. The result is a 16-char hex string.
func BrowserID(vec []float32) string {
	h := fnv.New64a()
	var buf [4]byte
	for _, x := range vec {
		binary.LittleEndian.PutUint32(buf[:], math.Float32bits(x))
		_, _ = h.Write(buf[:])
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)
}

// TierSimilarity returns a weighted Jaccard similarity (0..1, higher = more
// similar) over the (tier, key) values of two signal sets.
//
// For categorical signals (canvas, glext, audio, fonts, codecs, math, kbd,
// ua, uach) values must match exactly to count as overlap.
//
// For continuous signals (cores, mem, screen_w/h/depth, hz, tz, langs,
// quota, timing, battery) values match if they are within tolerance.
//
// weights[k] is the entropy contribution of (tier, key) k. Equal-weight is
// used when weights is nil.
func TierSimilarity(a, b []Signal) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	am := indexSignals(a)
	bm := indexSignals(b)
	// union of keys
	keys := make(map[string]struct{}, len(am)+len(bm))
	for k := range am {
		keys[k] = struct{}{}
	}
	for k := range bm {
		keys[k] = struct{}{}
	}
	var sumW, matchW float64
	for k := range keys {
		av, aok := am[k]
		bv, bok := bm[k]
		w := 1.0
		if c, ok := entropyWeights[k]; ok {
			w = c
		}
		sumW += w
		if !aok || !bok {
			continue
		}
		if matchValue(k, av, bv) {
			matchW += w
		}
	}
	if sumW == 0 {
		return 0
	}
	return matchW / sumW
}

// entropyWeights maps (tier, key) → entropy contribution (bits).
// Calibrated from the upstream index.html `ENTROPY_BITS` table
// (see glassbox-go/glassbox/probes-bundle.js). Higher = more
// discriminative, more weight in similarity.
var entropyWeights = map[string]float64{
	// hw
	"hw.cores":       2.0,
	"hw.mem":         1.5,
	"hw.screen":      4.8,
	"hw.hz":          1.3,
	"hw.tz":          3.3,
	"hw.locale":      2.4,
	"hw.langs":       2.8,
	"hw.touch":       1.0,
	"hw.dst":         0.4,
	"hw.plat":        1.4,
	"hw.glvendor":    2.5,
	"hw.glrend":      7.0,
	"hw.gpu":         2.5,
	// engine
	"engine.canvas":  8.5,
	"engine.glext":   2.8,
	"engine.audio":   5.2,
	"engine.fonts":   6.5,
	"engine.codecs":  2.6,
	"engine.math":    1.8,
	"engine.rects":   4.5,
	"engine.tmetrics": 3.8,
	// build
	"build.ua":       5.0,
	"build.uach":     2.6,
	"build.apis":     5.5,
	"build.voices":   4.8,
	"build.kbd":      1.8,
	"build.wasm":     2.5,
	"build.bot":      1.0,
	// session
	"session.css":        0.5,
	"session.timing":     2.5,
	"session.quota":      0.5,
	"session.net":        1.0,
	"session.battery":    1.5,
	"session.perms":      1.5,
	"session.webrtc":     3.0,
	"session.ip":         6.5,
	"session.ip_asn":     4.0,
	"session.cookies":    0.8,
	"session.devices":    1.0,
}

func indexSignals(sigs []Signal) map[string]string {
	out := make(map[string]string, len(sigs))
	for _, s := range sigs {
		out[s.Tier+"."+s.Key] = s.Value
	}
	return out
}

// matchValue returns true if two signal values should be considered equal.
// Exact match for categoricals, tolerance-based for continuous signals.
func matchValue(key, a, b string) bool {
	if a == b {
		return true
	}
	tol, isContinuous := continuousTol[key]
	if !isContinuous {
		return false
	}
	af, errA := strconv.ParseFloat(a, 64)
	bf, errB := strconv.ParseFloat(b, 64)
	if errA != nil || errB != nil {
		return false
	}
	return math.Abs(af-bf) <= tol
}

var continuousTol = map[string]float64{
	"hw.cores":         0,
	"hw.mem":           1,
	"hw.screen":        0, // special-cased below
	"hw.hz":            5,
	"hw.tz":            0, // exact match
	"hw.langs":         0,
	"session.quota":    0,
	"session.timing":   0,
	"session.battery":  0,
	"hw.glvendor":      0,
	"hw.glrend":        0,
	"hw.plat":          0,
}

// matchScreen treats screens as equal if width/height match (ignores depth/hz).
func matchScreen(a, b string) bool {
	pa := strings.SplitN(a, "x", 2)
	pb := strings.SplitN(b, "x", 2)
	if len(pa) < 2 || len(pb) < 2 {
		return a == b
	}
	return pa[0] == pb[0] && pa[1] == pb[1]
}

// writeBytes fills out[idx..idx+3] with 4 bytes of the FNV-64 hash of s.
// If s is empty, all four stay 0.
func writeBytes(out []float32, idx int, s string) {
	writeBytesFromString(out, idx, s)
}

func writeBytesFromString(out []float32, idx int, s string) {
	if s == "" {
		return
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	sum := h.Sum(nil)
	for i := 0; i < 4 && idx+i < len(out); i++ {
		out[idx+i] = float32(sum[i])
	}
}

// parseF parses a string as float; empty → 0.
func parseF(s string) float32 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return 0
	}
	return float32(f)
}

func parseScreen(s string) (w, h, d float32) {
	if s == "" {
		return
	}
	parts := strings.SplitN(s, "x", 3)
	if len(parts) >= 2 {
		w = parseF(parts[0])
		h = parseF(parts[1])
	}
	if len(parts) == 3 {
		depth := strings.SplitN(parts[2], "@", 2)[0]
		d = parseF(depth)
	}
	return
}

// tzHours converts an IANA timezone string into an approximate UTC offset
// (in hours, may be fractional) by looking up a small built-in table.
// Falls back to 0 for unknown zones.
func tzHours(tz string) float32 {
	if tz == "" {
		return 0
	}
	// Small embedded table for the most common zones. Augment as needed.
	// Values are standard-time offsets (DST handled separately via hw.dst).
	tzOffsets := map[string]float32{
		"UTC":                            0,
		"Etc/UTC":                        0,
		"America/Los_Angeles":            -8,
		"America/Denver":                 -7,
		"America/Chicago":                -6,
		"America/New_York":               -5,
		"America/Sao_Paulo":              -3,
		"America/Buenos_Aires":           -3,
		"America/Mexico_City":            -6,
		"Europe/London":                  0,
		"Europe/Paris":                   1,
		"Europe/Berlin":                  1,
		"Europe/Madrid":                  1,
		"Europe/Athens":                  2,
		"Europe/Moscow":                  3,
		"Africa/Johannesburg":            2,
		"Asia/Dubai":                     4,
		"Asia/Karachi":                   5,
		"Asia/Kolkata":                   5.5,
		"Asia/Bangkok":                   7,
		"Asia/Shanghai":                  8,
		"Asia/Tokyo":                     9,
		"Australia/Sydney":               10,
		"Auckland":                       12,
		"Pacific/Honolulu":               -10,
	}
	if off, ok := tzOffsets[tz]; ok {
		return off
	}
	return 0
}

func boolF(s string) float32 {
	if s == "true" || s == "1" {
		return 1
	}
	return 0
}

func parseCount(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Split(s, ","))
}
