package caddy

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	caddy "github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"golang.org/x/time/rate"
)

func init() {
	caddy.RegisterModule(&RateLimit{})
}

// RateLimit is a minimal per-client-IP token-bucket middleware for the
// embedded ingress. Winterflow targets small always-on machines; the point is
// not fairness engineering but a predefined ceiling so a traffic flood (or a
// misbehaving crawler) answers 429 instead of fanning every request into app
// containers and cooking the CPU. Caddy's standard distribution has no rate
// limiter; this in-repo module keeps the curated-module philosophy.
type RateLimit struct {
	// RPS is the sustained per-client-IP rate; <= 0 disables the middleware.
	RPS float64 `json:"rps,omitempty"`
	// Burst is the short-burst allowance (defaults to max(1, RPS)).
	Burst int `json:"burst,omitempty"`
	// TrustedProxies are the CIDRs (or bare IPs) of proxies in front of the
	// ingress — a Cloudflare tunnel's docker bridge, Cloudflare's edge.
	// Only a request whose TCP peer is inside one has its client IP taken
	// from CF-Connecting-IP / X-Forwarded-For; anyone else's headers are
	// ignored, so they can't be spoofed to dodge the limit. Empty: always
	// the TCP peer.
	TrustedProxies []string `json:"trusted_proxies,omitempty"`

	trusted  []netip.Prefix
	mu       sync.Mutex
	visitors map[string]*visitor
	done     chan struct{}
}

// visitor is one client IP's bucket plus its last activity for eviction.
type visitor struct {
	lim  *rate.Limiter
	seen time.Time
}

// maxVisitors hard-caps limiter memory (~100B each). Beyond it, new clients
// are admitted untracked (fail open) — memory safety beats precision under a
// spoofed-source flood.
const maxVisitors = 16384

// visitorIdleEviction is how long an IP may be silent before its bucket is
// reclaimed by the janitor.
const visitorIdleEviction = 3 * time.Minute

func (*RateLimit) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.winterflow_rate_limit",
		New: func() caddy.Module { return new(RateLimit) },
	}
}

// Provision initializes state and starts the idle-bucket janitor, which stops
// when this config generation is torn down (Cleanup).
func (rl *RateLimit) Provision(ctx caddy.Context) error {
	trusted, bad := ParseTrustedProxies(rl.TrustedProxies)
	if len(bad) > 0 {
		return fmt.Errorf("trusted_proxies: not an IP or CIDR: %s", strings.Join(bad, ", "))
	}
	rl.trusted = trusted
	rl.visitors = make(map[string]*visitor)
	rl.done = make(chan struct{})
	if rl.Burst <= 0 {
		rl.Burst = int(rl.RPS)
		if rl.Burst < 1 {
			rl.Burst = 1
		}
	}
	go rl.janitor()
	return nil
}

// Cleanup stops the janitor; Caddy calls it when a config reload replaces
// this handler instance.
func (rl *RateLimit) Cleanup() error {
	close(rl.done)
	return nil
}

func (rl *RateLimit) janitor() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-rl.done:
			return
		case now := <-ticker.C:
			rl.evictIdle(now)
		}
	}
}

func (rl *RateLimit) evictIdle(now time.Time) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for ip, v := range rl.visitors {
		if now.Sub(v.seen) > visitorIdleEviction {
			delete(rl.visitors, ip)
		}
	}
}

// limiterFor returns the client's bucket, creating it on first sight. nil
// means "don't track" (map at capacity).
func (rl *RateLimit) limiterFor(ip string, now time.Time) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if v, ok := rl.visitors[ip]; ok {
		v.seen = now
		return v.lim
	}
	if len(rl.visitors) >= maxVisitors {
		rl.evictIdleLocked(now)
		if len(rl.visitors) >= maxVisitors {
			return nil
		}
	}
	v := &visitor{lim: rate.NewLimiter(rate.Limit(rl.RPS), rl.Burst), seen: now}
	rl.visitors[ip] = v
	return v.lim
}

func (rl *RateLimit) evictIdleLocked(now time.Time) {
	for ip, v := range rl.visitors {
		if now.Sub(v.seen) > visitorIdleEviction {
			delete(rl.visitors, ip)
		}
	}
}

func (rl *RateLimit) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if rl.RPS <= 0 {
		return next.ServeHTTP(w, r)
	}
	lim := rl.limiterFor(rl.clientIP(r), time.Now())
	if lim != nil && !lim.Allow() {
		w.Header().Set("Retry-After", strconv.Itoa(1))
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("429 Too Many Requests\n"))
		return nil
	}
	return next.ServeHTTP(w, r)
}

// ParseTrustedProxies parses CIDRs or bare IPs (a host prefix), returning the
// valid ones and the entries that are neither.
func ParseTrustedProxies(entries []string) (valid []netip.Prefix, bad []string) {
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if p, err := netip.ParsePrefix(e); err == nil {
			valid = append(valid, p.Masked())
			continue
		}
		if a, err := netip.ParseAddr(e); err == nil {
			a = a.Unmap()
			valid = append(valid, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		bad = append(bad, e)
	}
	return valid, bad
}

func (rl *RateLimit) isTrusted(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range rl.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// clientIP is the address a request is throttled under: the TCP peer, unless
// that peer is a trusted proxy — then CF-Connecting-IP when it holds a valid
// IP, else the rightmost X-Forwarded-For entry that is not itself a trusted
// proxy (entries left of it are client-supplied and could be forged), else
// the peer. Headers from an untrusted peer are never read.
func (rl *RateLimit) clientIP(r *http.Request) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	peerAddr, err := netip.ParseAddr(peer)
	if err != nil || len(rl.trusted) == 0 || !rl.isTrusted(peerAddr) {
		return peer
	}
	if a, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); err == nil {
		return a.Unmap().String()
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break // a malformed hop: nothing left of it can be trusted
		}
		if !rl.isTrusted(a) {
			return a.Unmap().String()
		}
	}
	return peer
}

// Interface guards.
var (
	_ caddy.Provisioner           = (*RateLimit)(nil)
	_ caddy.CleanerUpper          = (*RateLimit)(nil)
	_ caddyhttp.MiddlewareHandler = (*RateLimit)(nil)
)
