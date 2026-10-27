package mcp

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"

	"winterflow/internal/app/web/middleware/realip"
)

// originKey carries the origin an MCP request came in on.
type originKey struct{}

// WithOrigin returns ctx carrying origin, the address the MCP client
// reached Winterflow at.
func WithOrigin(ctx context.Context, origin string) context.Context {
	return context.WithValue(ctx, originKey{}, origin)
}

// Origin is the origin the MCP request came in on ("" when unknown).
func Origin(ctx context.Context) string {
	v, _ := ctx.Value(originKey{}).(string)
	return v
}

// RequestOrigin is the origin r came in on: its Host header, with https
// when the connection was TLS or when the local proxy (a loopback peer,
// realip's trust rule) says so with X-Forwarded-Proto: https, else http.
// "" when the Host is not a plain host[:port].
func RequestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || (realip.PeerIsLoopback(r) && forwardedProto(r) == "https") {
		scheme = "https"
	}
	host := r.Host
	u, err := url.Parse(scheme + "://" + host)
	if host == "" || err != nil || u.Host != host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return scheme + "://" + host
}

// forwardedProto is the last X-Forwarded-Proto entry (the one the local
// proxy wrote), lower-cased.
func forwardedProto(r *http.Request) string {
	vals := r.Header.Values("X-Forwarded-Proto")
	if len(vals) == 0 {
		return ""
	}
	last := vals[len(vals)-1]
	return strings.ToLower(strings.TrimSpace(last[strings.LastIndex(last, ",")+1:]))
}

// IsLoopbackOrigin reports whether origin's host is localhost or a
// loopback address: a link on it opens only on Winterflow's own machine.
func IsLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// withOrigin puts the request's origin into its context for the tools.
func withOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(WithOrigin(r.Context(), RequestOrigin(r))))
	})
}
