package glassbox

import (
	"net/http"
	"regexp"

	"github.com/gorilla/websocket"
)

// wsUpgrader returns a gorilla/websocket upgrader configured for our use.
func wsUpgrader() websocket.Upgrader {
	return websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}
}

var (
	glMaskedRe = regexp.MustCompile(`(?i)swiftshader|llvmpipe|generic|or\s+similar|angle\s+\(google`)
	firefoxRe  = regexp.MustCompile(`(?i)firefox`)
)

// hybridValidate re-runs the masking heuristics that the client should
// not be able to fake and returns the server's authoritative answer.
func hybridValidate(p *Payload, _ bool) Masked {
	var m Masked
	if p == nil {
		return m
	}
	report := p.Entropy.Masked
	raw := p.Tiers
	// gl_renderer is the source of truth for glMasked.
	if glv, ok := raw["engine"]["glrend"]; ok && glMaskedRe.MatchString(glv) {
		m.GLMasked = true
	} else if glv, ok := raw["hw"]["glrend"]; ok && glMaskedRe.MatchString(glv) {
		m.GLMasked = true
	} else {
		m.GLMasked = report.GLMasked
	}
	// tz spoofed requires Intl.tz == "UTC".
	if tz, ok := raw["hw"]["tz"]; ok && tz == "UTC" {
		m.TZSpoofed = true
	} else {
		m.TZSpoofed = report.TZSpoofed
	}
	m.CanvasRand = report.CanvasRand
	m.CanvasSpoof = report.CanvasSpoof
	// RFP composite: Firefox AND tz_spoofed AND (canvas-spoof or canvas-rand or gl-masked).
	ff := firefoxRe.MatchString(p.UA)
	if ff && m.TZSpoofed && (m.CanvasSpoof || m.CanvasRand || m.GLMasked) {
		m.RFP = true
	} else {
		m.RFP = report.RFP
	}
	return m
}
