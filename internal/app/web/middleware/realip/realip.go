// Package realip sets a request's RemoteAddr to the client's address. Unlike
// chi's RealIP it reads only the last X-Forwarded-For entry (the hop the local
// proxy wrote), and only when the direct peer is a loopback address (the
// documented deployment binds the API to 127.0.0.1 behind Caddy or the
// embedded ingress). X-Real-IP is ignored: a plain reverse proxy passes a
// client-supplied one through unchanged, which would let any client pick its
// own address and bypass every per-IP limit. The proxy must set
// X-Forwarded-For to the real client (Caddy: header_up X-Forwarded-For
// {client_ip}).
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

// forwarded returns the last X-Forwarded-For entry (the one the local proxy
// wrote) when it is a valid IP address.
func forwarded(r *http.Request) string {
	var v string
	if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
		last := xff[len(xff)-1]
		v = strings.TrimSpace(last[strings.LastIndex(last, ",")+1:])
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
