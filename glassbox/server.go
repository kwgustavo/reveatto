package glassbox

import (
	"bytes"
	"io"
	"net/http"
	"strings"
)

// wrap returns an http.Handler that buffers HTML responses and injects
// the probes <script>. Other content types are streamed through unchanged.
func (h *Handler) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip buffering for WebSocket and script endpoints to allow hijacking.
		if r.URL.Path == h.opts.WSPath || r.URL.Path == h.opts.ScriptPath {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get(h.opts.SkipHeader) == "1" {
			next.ServeHTTP(w, r)
			return
		}
		bw := &bufferedResponse{
			h:      h,
			req:    r,
			header: w.Header().Clone(),
			up:     w,
		}
		next.ServeHTTP(bw, r)
		bw.commit()
	})
}

// bufferedResponse wraps http.ResponseWriter so we can rewrite HTML bodies.
type bufferedResponse struct {
	h          *Handler
	req        *http.Request
	up         http.ResponseWriter
	header     http.Header
	body       bytes.Buffer
	status     int
	wroteH     bool
	firstWrite bool
	err        error
	trickle    io.Writer
}

func (b *bufferedResponse) Header() http.Header { return b.header }

func (b *bufferedResponse) WriteHeader(code int) {
	if b.wroteH {
		return
	}
	b.status = code
	b.wroteH = true
}

func (b *bufferedResponse) Write(p []byte) (int, error) {
	if !b.wroteH {
		b.WriteHeader(http.StatusOK)
	}
	b.firstWrite = true
	if b.err != nil {
		return 0, b.err
	}
	if b.overflowing(len(p)) {
		if !b.startTrickle() {
			b.err = io.ErrShortWrite
			return 0, b.err
		}
		return b.trickle.Write(p)
	}
	return b.body.Write(p)
}

// Flush is a no-op on the buffered implementation; we need the whole body
// to decide on injection.
func (b *bufferedResponse) Flush() {}

// Unwrap exposes the underlying writer for http.NewResponseController.
func (b *bufferedResponse) Unwrap() http.ResponseWriter { return b.up }

// startTrickle finalises the upstream headers once and starts streaming
// the body to it without further buffering.
func (b *bufferedResponse) startTrickle() bool {
	if b.trickle != nil {
		return true
	}
	b.commitHeaders(b.up)
	b.trickle = b.up
	return true
}

func (b *bufferedResponse) commitHeaders(w http.ResponseWriter) {
	for k, v := range b.header {
		w.Header()[k] = v
	}
	status := b.status
	if !b.wroteH || status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
}

func (b *bufferedResponse) overflowing(n int) bool {
	return int64(b.body.Len())+int64(n) > b.h.opts.MaxBodyBytes
}

// commit is called by wrap after the inner handler finishes.
func (b *bufferedResponse) commit() {
	if b.trickle != nil {
		return
	}
	ct := strings.ToLower(b.header.Get("Content-Type"))
	ctype := strings.SplitN(ct, ";", 2)[0]

	if !shouldInject(ctype, b.h.opts.InjectTargets) {
		b.commitHeaders(b.up)
		if b.body.Len() > 0 {
			if _, err := b.up.Write(b.body.Bytes()); err != nil {
				b.h.logger.Printf("glassbox: passthrough: %v", err)
			}
		}
		return
	}
	original := b.body.Bytes()
	body := injectScript(original, b.h.opts.GeoLookup)
	b.h.logger.Printf("glassbox: original=%d injected=%d", len(original), len(body))
	// Remove Content-Encoding and Content-Length so we can send chunked if needed.
	b.up.Header().Del("Content-Encoding")
	b.up.Header().Del("Content-Length")
	// Also remove from the buffered header so commitHeaders doesn't restore it.
	b.header.Del("Content-Encoding")
	b.header.Del("Content-Length")
	b.commitHeaders(b.up)
	if _, err := b.up.Write(body); err != nil {
		b.h.logger.Printf("glassbox: commit body: %v", err)
	}
}
