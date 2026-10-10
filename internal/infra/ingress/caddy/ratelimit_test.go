package caddy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	caddy "github.com/caddyserver/caddy/v2"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/ingress/domains"
	"winterflow/internal/infra/ingress/domains/certtest"
)

// nextCounter counts how many requests pass through the middleware.
type nextCounter struct{ calls int }

func (n *nextCounter) ServeHTTP(http.ResponseWriter, *http.Request) error {
	n.calls++
	return nil
}

func newTestLimiter(rps float64, burst int) *RateLimit {
	return &RateLimit{
		RPS:      rps,
		Burst:    burst,
		visitors: make(map[string]*visitor),
		done:     make(chan struct{}),
	}
}

func doReq(t *testing.T, rl *RateLimit, next *nextCounter, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	if err := rl.ServeHTTP(w, r, next); err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}
	return w
}

func TestRateLimitAllowsWithinBurstThenRejects(t *testing.T) {
	rl := newTestLimiter(1, 3)
	next := &nextCounter{}

	for i := 0; i < 3; i++ {
		if w := doReq(t, rl, next, "203.0.113.7:1234"); w.Code != http.StatusOK {
			t.Fatalf("request %d within burst: status %d", i, w.Code)
		}
	}
	w := doReq(t, rl, next, "203.0.113.7:1234")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("beyond burst: status %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("429 must carry Retry-After")
	}
	if next.calls != 3 {
		t.Fatalf("next called %d times, want 3", next.calls)
	}
}

func TestRateLimitIsPerClientIP(t *testing.T) {
	rl := newTestLimiter(1, 1)
	next := &nextCounter{}

	if w := doReq(t, rl, next, "203.0.113.7:1"); w.Code != http.StatusOK {
		t.Fatalf("first client: %d", w.Code)
	}
	if w := doReq(t, rl, next, "203.0.113.7:2"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("same IP, different port must share the bucket: %d", w.Code)
	}
	// A different IP has its own untouched bucket.
	if w := doReq(t, rl, next, "198.51.100.9:1"); w.Code != http.StatusOK {
		t.Fatalf("second client must not be throttled: %d", w.Code)
	}
}

func TestRateLimitDisabledWhenZero(t *testing.T) {
	rl := newTestLimiter(0, 0)
	next := &nextCounter{}
	for i := 0; i < 50; i++ {
		if w := doReq(t, rl, next, "203.0.113.7:1"); w.Code != http.StatusOK {
			t.Fatalf("disabled limiter throttled request %d: %d", i, w.Code)
		}
	}
	if next.calls != 50 {
		t.Fatalf("next calls = %d", next.calls)
	}
}

func TestRateLimitEvictsIdleVisitors(t *testing.T) {
	rl := newTestLimiter(1, 1)
	next := &nextCounter{}
	doReq(t, rl, next, "203.0.113.7:1")
	doReq(t, rl, next, "198.51.100.9:1")
	if len(rl.visitors) != 2 {
		t.Fatalf("visitors = %d, want 2", len(rl.visitors))
	}
	rl.evictIdle(time.Now().Add(visitorIdleEviction + time.Minute))
	if len(rl.visitors) != 0 {
		t.Fatalf("idle visitors not evicted: %d left", len(rl.visitors))
	}
}

func TestRateLimitCapsVisitorMap(t *testing.T) {
	rl := newTestLimiter(1, 1)
	// Fill to the cap with fresh (non-evictable) entries.
	now := time.Now()
	for i := 0; i < maxVisitors; i++ {
		rl.visitors[string(rune(i))+"x"] = &visitor{lim: nil, seen: now}
	}
	// A new client fails open (untracked) instead of growing the map.
	if lim := rl.limiterFor("203.0.113.99", now); lim != nil {
		t.Fatal("at capacity, limiterFor must return nil (fail open)")
	}
	if len(rl.visitors) != maxVisitors {
		t.Fatalf("map grew past cap: %d", len(rl.visitors))
	}
}

// TestBuildConfigValidatesWithCaddy runs the full emitted config — throttle
// module, timeouts, proxy routes, all three TLS modes with an uploaded
// certificate loaded from disk, the wildcard catch-all's "not" matcher and
// skip_certificates — through Caddy's own validator, so a schema mistake,
// an unloadable certificate or an unregistered module fails here instead of
// at agent startup.
func TestBuildConfigValidatesWithCaddy(t *testing.T) {
	dir := t.TempDir()
	store := domains.NewStore(dir)
	cert, key := certtest.Issue(t, time.Now().Add(90*24*time.Hour), "*.cert.test")
	for _, r := range []struct {
		name      string
		mode      command.DomainTLS
		cert, key []byte
	}{
		{"*.cert.test", command.DomainTLSCertificate, cert, key},
		{"x.cert.test", command.DomainTLSOff, nil, nil},
		{"example.test", command.DomainTLSAuto, nil, nil},
		{"*.auto.test", command.DomainTLSAuto, nil, nil},
		{"*.tunnel.test", command.DomainTLSOff, nil, nil},
	} {
		if err := store.Put(r.name, r.mode, r.cert, r.key); err != nil {
			t.Fatal(err)
		}
	}
	regs, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	apps := []AppIngress{{
		AppID: "app1",
		Ingress: model.Ingress{
			Domains: []model.IngressDomain{
				{Domain: "example.test", UpstreamPort: 8080},
				{Domain: "a.cert.test", UpstreamPort: 8081},
				{Domain: "b.tunnel.test", UpstreamPort: 8082},
			},
		},
	}}
	raw, warnings, err := BuildConfig(apps, regs, Options{
		RateLimitRPS:   50,
		RateLimitBurst: 100,
		StorageDir:     t.TempDir(),
	})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("BuildConfig: err=%v warnings=%v", err, warnings)
	}
	var cfg caddy.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("emitted config is not valid JSON for caddy.Config: %v", err)
	}
	if err := caddy.Validate(&cfg); err != nil {
		t.Fatalf("caddy rejects the emitted config: %v", err)
	}
}

// TestBuildConfigIncludesThrottle fixates that the predefined throttle sits in
// front of every route on both servers, and that connection timeouts are set.
func TestBuildConfigIncludesThrottle(t *testing.T) {
	raw, warnings, err := BuildConfig(nil, nil, Options{RateLimitRPS: 50, RateLimitBurst: 100})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("BuildConfig: err=%v warnings=%v", err, warnings)
	}
	var cfg struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Routes []struct {
						Handle []map[string]any `json:"handle"`
					} `json:"routes"`
					ReadHeaderTimeout string `json:"read_header_timeout"`
					IdleTimeout       string `json:"idle_timeout"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	for name, srv := range cfg.Apps.HTTP.Servers {
		if len(srv.Routes) == 0 {
			t.Fatalf("server %s: no routes", name)
		}
		h := srv.Routes[0].Handle[0]
		if h["handler"] != "winterflow_rate_limit" {
			t.Errorf("server %s: first route handler = %v, want winterflow_rate_limit", name, h["handler"])
		}
		if h["rps"] != float64(50) || h["burst"] != float64(100) {
			t.Errorf("server %s: throttle params = %v", name, h)
		}
		if srv.ReadHeaderTimeout == "" || srv.IdleTimeout == "" {
			t.Errorf("server %s: missing connection timeouts", name)
		}
	}

	// Disabled: no throttle route.
	raw, _, err = BuildConfig(nil, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	for name, srv := range cfg.Apps.HTTP.Servers {
		for _, rt := range srv.Routes {
			for _, h := range rt.Handle {
				if h["handler"] == "winterflow_rate_limit" {
					t.Errorf("server %s: throttle present with rps=0", name)
				}
			}
		}
	}
}
