package glassbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// serveScript serves the embedded probes.js bundle.
func (h *Handler) serveScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(h.script)
}

// handleWS upgrades to a WebSocket and reads one Payload message.
func (h *Handler) handleWS(w http.ResponseWriter, r *http.Request) {
	if !h.opts.AllowAnyOrigin {
		origin := r.Header.Get("Origin")
		if origin != "" && !originAllowed(origin, r.Host) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
	}
	cookieSID := h.readSessionCookie(r)
	upgrader := wsUpgrader()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote a response.
		h.logger.Printf("glassbox: ws upgrade: %v", err)
		return
	}
	defer conn.Close()
	// ensure session cookie exists (issue one on upgrade).
	setCookie := false
	if cookieSID == "" {
		cookieSID = newSessionID()
		setCookie = true
	}
	conn.SetReadLimit(2 << 20) // 2 MiB

	msgType, raw, err := conn.ReadMessage()
	if err != nil {
		if !errors.Is(err, io.EOF) {
			h.logger.Printf("glassbox: ws read: %v", err)
		}
		return
	}
	if msgType != 1 {
		_ = conn.WriteJSON(map[string]any{"ok": false, "error": "expected text frame"})
		return
	}
	payload, err := parsePayload(raw)
	if err != nil {
		_ = conn.WriteJSON(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if strings.TrimSpace(payload.Session) == "" {
		payload.Session = cookieSID
	}
	sigs := payload.Signals()
	mv := h.buildVisit(r, payload, sigs)
	id, err := h.store.Save(r.Context(), mv, sigs)
	if err != nil {
		h.logger.Printf("glassbox: save visit: %v", err)
		_ = conn.WriteJSON(map[string]any{"ok": false, "error": "persist"})
		return
	}
	if setCookie {
		_ = w  // already upgraded; cookies must travel on upgrade response — set in upgrade header.
	}
	_ = conn.WriteJSON(map[string]any{"ok": true, "visit_id": id, "session": payload.Session})
}

func (h *Handler) buildVisit(r *http.Request, p *Payload, sigs []Signal) Visit {
	now := nowMillis()
	v := Visit{
		SessionID: p.Session,
		FirstSeen: now,
		LastSeen:  now,
		UA:        p.UA,
		Pct:       p.Entropy.Pct,
		Bits:      p.Entropy.Bits,
		Band:      p.Entropy.Band,
		Country:   p.GeoCountry,
		Vec:       ExtractVector(sigs),
	}
	v.Masked = hybridValidate(p, h.opts.AllowAnyOrigin)
	if h.opts.RecordIP {
		v.IP = clientIP(r)
	}
	v.Raw = string(p.Raw)
	return v
}

func (h *Handler) readSessionCookie(r *http.Request) string {
	c, err := r.Cookie(h.opts.SessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// Payload is the JSON shape sent by the browser after probes finish.
type Payload struct {
	V          int                          `json:"v"`
	Session    string                       `json:"session"`
	UA         string                       `json:"ua"`
	Lang       string                       `json:"lang"`
	Tiers      map[string]map[string]string `json:"tiers"`
	Entropy    Entropy                      `json:"entropy"`
	GeoCountry string                       `json:"geo_country,omitempty"`
	Raw        json.RawMessage              `json:"-"`
}

// Entropy is the identifiability estimate from index.html.
type Entropy struct {
	Bits  float64    `json:"bits"`
	Pct   float64    `json:"pct"`
	Band  string     `json:"band"`
	OneIn float64    `json:"oneIn"`
	Masked MaskedJSON `json:"masked"`
}

// MaskedJSON matches the client's reported masking flags (used only for
// storage; truth comes from server re-validation).
type MaskedJSON struct {
	RFP         bool `json:"rfp"`
	CanvasRand  bool `json:"canvasRand"`
	CanvasSpoof bool `json:"canvasSpoofed"`
	GLMasked    bool `json:"glMasked"`
	TZSpoofed   bool `json:"tzSpoofed"`
}

// Signals flattens the payload's tier map into []Signal rows.
func (p *Payload) Signals() []Signal {
	if p == nil {
		return nil
	}
	out := make([]Signal, 0, 64)
	for _, tier := range []string{"hw", "engine", "build", "session"} {
		m := p.Tiers[tier]
		for k, v := range m {
			out = append(out, Signal{Tier: tier, Key: k, Value: v})
		}
	}
	return out
}

func parsePayload(raw []byte) (*Payload, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var p Payload
	if err := dec.Decode(&p); err != nil {
		return nil, err
	}
	p.Raw = append(p.Raw[:0], raw...)
	return &p, nil
}

func originAllowed(origin, host string) bool {
	if origin == "" {
		return true
	}
	if i := strings.Index(origin, "://"); i >= 0 {
		origin = origin[i+3:]
	}
	if k := strings.Index(origin, "/"); k >= 0 {
		origin = origin[:k]
	}
	if k := strings.Index(host, ":"); k >= 0 {
		host = host[:k]
	}
	if o := strings.Index(origin, ":"); o >= 0 {
		origin = origin[:o]
	}
	return strings.EqualFold(origin, host)
}
