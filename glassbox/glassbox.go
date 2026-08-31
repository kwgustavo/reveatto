// Package glassbox is a drop-in middleware that fingerprints every visitor
// to a Go web application and stores the result as a queryable characteristic
// vector in SQLite.
//
// The browser-side probes are ported from the upstream GlassBox project
// (https://glassbox.codecanary.org). On every HTML response the library
// injects a <script> that runs the probes and posts a single JSON message
// back over WebSocket.
//
// Typical usage:
//
//	mux := http.NewServeMux()
//	mux.Handle("/app", myHandler)
//	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(...)))
//	gb, _ := glassbox.New(glassbox.Options{DBPath: "glassbox.db"})
//	defer gb.Close()
//	http.ListenAndServe(":8080", gb.Wrap(mux))
package glassbox

import (
	"errors"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
)

// Options configures the GlassBox middleware.
type Options struct {
	// DBPath is the SQLite database file. Default: "glassbox.db".
	DBPath string
	// RecordIP, if true, stores client IP in the visits table. Default false
	// to mirror GlassBox's privacy stance.
	RecordIP bool
	// AllowAnyOrigin disables the WS Origin check. Default false (CSRF-safe).
	AllowAnyOrigin bool
	// GeoLookup, if true, sets window.__GLASSBOX_GEO__ = true on every
	// injected page, enabling probe 26 (public IP via ipapi/ipwho).
	// Default false — opt-in because it is the only probe that hits the network.
	GeoLookup bool
	// MaxBodyBytes caps the size of HTML responses that will be re-written
	// to inject the probes script. Default 5 MiB. Responses larger are
	// passed through unchanged.
	MaxBodyBytes int64
	// InjectTargets lists content-types that the middleware will rewrite.
	// Default: {"text/html", "application/xhtml+xml"}.
	InjectTargets []string
	// Log overrides the default logger.
	Log *log.Logger
	// SkipHeader is a request header name; if set to "1", injection is
	// skipped for that request. Default "X-GlassBox-Skip".
	SkipHeader string
	// WSPath overrides the WebSocket endpoint path. Default "/__glassbox/ws".
	WSPath string
	// ScriptPath overrides the script endpoint. Default "/__glassbox/probes.js".
	ScriptPath string
	// SessionCookieName overrides "gb_sess".
	SessionCookieName string
}

// Handler holds middleware state.
type Handler struct {
	opts   Options
	store  *Store
	script []byte // JS bundle to inject
	logger *log.Logger
}

// New constructs a Handler bound to opts and opens the underlying database.
func New(opts Options) (*Handler, error) {
	h := &Handler{opts: opts}
	if h.opts.DBPath == "" {
		h.opts.DBPath = "glassbox.db"
	}
	if h.opts.MaxBodyBytes <= 0 {
		h.opts.MaxBodyBytes = 5 * 1024 * 1024
	}
	if len(h.opts.InjectTargets) == 0 {
		h.opts.InjectTargets = []string{"text/html", "application/xhtml+xml"}
	}
	if h.opts.WSPath == "" {
		h.opts.WSPath = "/__glassbox/ws"
	}
	if h.opts.ScriptPath == "" {
		h.opts.ScriptPath = "/__glassbox/probes.js"
	}
	if h.opts.SessionCookieName == "" {
		h.opts.SessionCookieName = "gb_sess"
	}
	if h.opts.SkipHeader == "" {
		h.opts.SkipHeader = "X-GlassBox-Skip"
	}
	if h.opts.Log != nil {
		h.logger = h.opts.Log
	} else {
		h.logger = log.Default()
	}
	var err error
	h.store, err = Open(h.opts.DBPath)
	if err != nil {
		return nil, err
	}
	h.script = probesJSBytes()
	return h, nil
}

// Store exposes the underlying SQLite store.
func (h *Handler) Store() *Store { return h.store }

// Close releases the database.
func (h *Handler) Close() error {
	if h.store == nil {
		return nil
	}
	return h.store.Close()
}

// Mount wires GlassBox's WS endpoint and script endpoint into mux.
//
// Use this when you already have a mux with a "/" catch-all registered
// and want to add GlassBox without taking over routing. You must wrap
// the handlers yourself with Wrap.
//
//	gb.Mount(mux)
//	http.ListenAndServe(":8080", gb.Wrap(mux))
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.Handle(h.opts.ScriptPath, http.HandlerFunc(h.serveScript))
	mux.Handle(h.opts.WSPath, http.HandlerFunc(h.handleWS))
}

// WrapMux is the convenience one-liner: registers GlassBox paths on
// mux and returns an http.Handler that injects HTML plus delegates
// every other path to mux.
//
//	gb.WrapMux(mux)
func (h *Handler) WrapMux(mux *http.ServeMux) http.Handler {
	h.Mount(mux)
	return h.Wrap(mux)
}

// Wrap returns an http.Handler that runs the HTML-injection middleware
// around next.
func (h *Handler) Wrap(next http.Handler) http.Handler {
	return h.wrap(next)
}

// FileServer returns an http.Handler that serves files from dir and runs
// GlassBox injection around every HTML response. Equivalent to
// http.FileServer(http.Dir(dir)) wrapped with Wrap.
func (h *Handler) FileServer(dir string) (http.Handler, error) {
	if dir == "" {
		return nil, errors.New("glassbox: empty dir")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	return h.Wrap(http.FileServer(http.Dir(abs))), nil
}

// fileServerFromFS is kept available for callers that use fs.FS.
func (h *Handler) fileServerFromFS(fsys fs.FS) http.Handler {
	return h.Wrap(http.FileServer(http.FS(fsys)))
}
