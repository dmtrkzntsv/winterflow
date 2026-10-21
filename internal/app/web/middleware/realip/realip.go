// Package realip sets a request's RemoteAddr to the client's address. Unlike
// chi's RealIP it trusts X-Real-IP / X-Forwarded-For only when the direct
// peer is a loopback address (the documented deployment binds the API to
// 127.0.0.1 behind Caddy or the embedded ingress); from anywhere else those
// headers are client-controlled and would let any per-IP limit be bypassed.
package realip

import (
	"net"
	"net/http"
	"strings"
)

// Middleware rewrites r.RemoteAddr to the forwarded client address when the
// peer is loopback and a valid one is given; otherwise it leaves it alone.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := net.ParseIP(ClientIP(r)); ip != nil && ip.IsLoopback() {
			if fwd := forwarded(r); fwd != "" {
				r.RemoteAddr = fwd
			}
		}
		next.ServeHTTP(w, r)
	})
}

// forwarded returns X-Real-IP, else the last X-Forwarded-For entry (the one
// the proxy appended), when it is a valid IP address.
func forwarded(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("X-Real-IP"))
	if v == "" {
		if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
			last := xff[len(xff)-1]
			v = strings.TrimSpace(last[strings.LastIndex(last, ",")+1:])
		}
	}
	if ip := net.ParseIP(v); ip != nil {
		return ip.String()
	}
	return ""
}

// ClientIP is the host part of r.RemoteAddr (which may carry no port).
func ClientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
