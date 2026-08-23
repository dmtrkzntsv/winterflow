// Package ratelimit provides a small per-client-IP token-bucket middleware.
// It guards the auth surface: bcrypt verification is by far the most
// CPU-expensive request the API serves, so credential-stuffing or plain
// abuse must hit a 429 wall instead of burning cores.
package ratelimit

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// maxVisitors caps limiter memory; beyond it new clients pass untracked
// (memory safety beats precision under a spoofed-source flood).
const maxVisitors = 16384

// idleEviction is how long an IP may be silent before its bucket is
// reclaimed.
const idleEviction = 3 * time.Minute

type visitor struct {
	lim  *rate.Limiter
	seen time.Time
}

// Limiter is a per-IP token bucket usable as HTTP middleware. The zero value
// is not usable; construct with New.
type Limiter struct {
	rps   float64
	burst int

	mu        sync.Mutex
	visitors  map[string]*visitor
	lastSweep time.Time
}

// New builds a limiter allowing rps sustained requests with the given burst
// per client IP.
func New(rps float64, burst int) *Limiter {
	return &Limiter{rps: rps, burst: burst, visitors: make(map[string]*visitor)}
}

// Allow reports whether a request from ip may proceed now. Sweeping of idle
// buckets happens inline (amortized) — no background goroutine to manage.
func (l *Limiter) Allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastSweep) > time.Minute {
		l.lastSweep = now
		for k, v := range l.visitors {
			if now.Sub(v.seen) > idleEviction {
				delete(l.visitors, k)
			}
		}
	}

	v, ok := l.visitors[ip]
	if !ok {
		if len(l.visitors) >= maxVisitors {
			return true // fail open, see maxVisitors
		}
		v = &visitor{lim: rate.NewLimiter(rate.Limit(l.rps), l.burst)}
		l.visitors[ip] = v
	}
	v.seen = now
	return v.lim.Allow()
}

// Middleware wraps a handler with the per-IP limit, answering 429 when
// exceeded. Note: behind the embedded ingress or a tunnel every request
// shares the proxy's IP, which degrades this into a global cap on the auth
// surface — for its purpose (bounding bcrypt CPU) that is exactly as good.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if !l.Allow(ip, time.Now()) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
