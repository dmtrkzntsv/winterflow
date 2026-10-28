// Package userlimit is a per-user token-bucket middleware for authenticated
// routes whose callers share egress IPs (MCP clients run by an AI provider,
// say), where a per-IP limit would throttle unrelated users together. It
// runs behind the auth middleware and keys each bucket by the authenticated
// user id.
package userlimit

import (
	"net/http"
	"time"

	"winterflow/internal/app/web/middleware/ratelimit"
	webutil "winterflow/internal/app/web/util"
)

// Limiter is a per-user token bucket; construct with New.
type Limiter struct {
	buckets *ratelimit.Limiter
}

// New allows rps sustained requests with the given burst per user.
func New(rps float64, burst int) *Limiter {
	return &Limiter{buckets: ratelimit.New(rps, burst)}
}

// Middleware answers 429 once the caller's bucket is empty. A request the
// auth middleware did not authenticate is refused with 401: the limiter
// must sit behind it, and an unwired chain fails closed.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := webutil.Principal(r)
		if !ok || p.UserID == "" {
			webutil.Unauthorized(w)
			return
		}
		if !l.buckets.Allow(p.UserID, time.Now()) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
