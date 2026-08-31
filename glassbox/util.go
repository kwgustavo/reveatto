package glassbox

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"
)

func nowMillis() time.Time { return time.Now() }

func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// sessionCookieHeader returns a Set-Cookie value suitable for a
// session-cookie issued at WS upgrade.
func sessionCookieHeader(name, value string) string {
	var b strings.Builder
	b.WriteString(name)
	b.WriteString("=")
	b.WriteString(value)
	b.WriteString("; Path=/; SameSite=Lax; HttpOnly")
	return b.String()
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
