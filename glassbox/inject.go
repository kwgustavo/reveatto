package glassbox

import (
	"bytes"
	"io"
	"strings"
)

// injectScript adds the GlassBox probe <script> tags right before </head>
// (or before <body>, or at the start of the document). It is idempotent: if
// a previous marker comment is found, the existing scripts are dropped.
//
// When geoLookup is true, the bundle is given window.__GLASSBOX_GEO__ = true,
// enabling probe 26 (public IP — the only probe that hits the network).
func injectScript(body []byte, geoLookup bool) []byte {
	const marker = "<!-- __glassbox_injected__ -->"
	if bytes.Contains(body, []byte(marker)) {
		body = stripInjected(body)
	}
	geoLine := ""
	if geoLookup {
		geoLine = `<script>window.__GLASSBOX_GEO__=true;</script>` + "\n"
	}
	scripts := []byte(
		marker + "\n" +
			`<script>window.__GLASSBOX_WS_URL=(location.protocol==="https:"?"wss://":"ws://")+location.host+"/__glassbox/ws";</script>` + "\n" +
			geoLine +
			`<script src="/__glassbox/probes.js" defer></script>` + "\n",
	)

	// Prefer </head>, then <body, then document start.
	lower := bytes.ToLower(body)
	if i := bytes.Index(lower, []byte("</head>")); i >= 0 {
		out := make([]byte, 0, len(body)+len(scripts))
		out = append(out, body[:i]...)
		out = append(out, scripts...)
		out = append(out, body[i:]...)
		return out
	}
	if i := bytes.Index(lower, []byte("<body")); i >= 0 {
		j := i + bytes.IndexByte(body[i:], '>')
		if j > i {
			cut := i + j + 1
			out := make([]byte, 0, len(body)+len(scripts))
			out = append(out, body[:cut]...)
			out = append(out, scripts...)
			out = append(out, body[cut:]...)
			return out
		}
	}
	out := make([]byte, 0, len(body)+len(scripts))
	out = append(out, scripts...)
	out = append(out, body...)
	return out
}

// stripInjected removes the previously-injected block (best effort).
func stripInjected(body []byte) []byte {
	const marker = "<!-- __glassbox_injected__ -->"
	start := bytes.Index(body, []byte(marker))
	if start < 0 {
		return body
	}
	end := start
	if i := bytes.Index(body[start:], []byte("</script>")); i >= 0 {
		end = start + i + len("</script>")
		if end < len(body) && body[end] == '\n' {
			end++
		}
	} else {
		end = len(body)
	}
	out := make([]byte, 0, len(body)-(end-start))
	out = append(out, body[:start]...)
	out = append(out, body[end:]...)
	return out
}

// shouldInject consults the response Content-Type and returns true if
// injection is appropriate.
func shouldInject(ct string, allowed []string) bool {
	if ct == "" {
		return false
	}
	ct = strings.ToLower(strings.SplitN(ct, ";", 2)[0])
	ct = strings.TrimSpace(ct)
	for _, want := range allowed {
		if ct == want {
			return true
		}
	}
	return false
}

// itoa is a tiny formatting helper so we don't pull in strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// compiles.io.Unused so the import survives.
var _ = io.Copy
