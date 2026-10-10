package caddy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
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

func netipMust(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
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
		TrustedProxies: []string{"172.16.0.0/12", "2400:cb00::/32"},
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

// trustingLimiter is a 1-request limiter that trusts the given proxies.
func trustingLimiter(t *testing.T, proxies ...string) *RateLimit {
	t.Helper()
	rl := newTestLimiter(1, 1)
	trusted, bad := ParseTrustedProxies(proxies)
	if len(bad) > 0 {
		t.Fatalf("bad proxies %v", bad)
	}
	rl.trusted = trusted
	return rl
}

func keyOf(rl *RateLimit, remote string, headers map[string]string) string {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return rl.clientIP(r)
}

func TestRateLimitClientIPFromTrustedProxies(t *testing.T) {
	rl := trustingLimiter(t, "172.16.0.0/12", "10.0.0.1")
	tunnel := "172.17.0.2:40000" // cloudflared on the docker bridge
	for _, tt := range []struct {
		name    string
		remote  string
		headers map[string]string
		want    string
	}{
		{"untrusted peer: spoofed headers ignored", "203.0.113.7:1",
			map[string]string{"CF-Connecting-IP": "198.51.100.1", "X-Forwarded-For": "198.51.100.2"}, "203.0.113.7"},
		{"trusted peer: CF-Connecting-IP", tunnel,
			map[string]string{"CF-Connecting-IP": " 198.51.100.1 ", "X-Forwarded-For": "198.51.100.2"}, "198.51.100.1"},
		{"bare-IP trusted proxy", "10.0.0.1:5", map[string]string{"CF-Connecting-IP": "2001:db8::1"}, "2001:db8::1"},
		{"IPv4-mapped trusted peer", "[::ffff:172.17.0.2]:1", map[string]string{"CF-Connecting-IP": "198.51.100.1"}, "198.51.100.1"},
		{"bad CF-Connecting-IP: rightmost untrusted XFF hop", tunnel,
			map[string]string{"CF-Connecting-IP": "not-an-ip", "X-Forwarded-For": "1.1.1.1, 198.51.100.2, 172.18.0.9"}, "198.51.100.2"},
		{"malformed rightmost XFF hop: the peer", tunnel, map[string]string{"X-Forwarded-For": "198.51.100.2, garbage"}, "172.17.0.2"},
		{"XFF of trusted hops only: the peer", tunnel, map[string]string{"X-Forwarded-For": "172.20.0.1"}, "172.17.0.2"},
		{"no headers: the peer", tunnel, nil, "172.17.0.2"},
		{"peer without a port", "172.17.0.2", map[string]string{"CF-Connecting-IP": "198.51.100.1"}, "198.51.100.1"},
	} {
		if got := keyOf(rl, tt.remote, tt.headers); got != tt.want {
			t.Errorf("%s: keyed on %q, want %q", tt.name, got, tt.want)
		}
	}
	// Without trusted proxies the headers are never read.
	if got := keyOf(newTestLimiter(1, 1), tunnel, map[string]string{"CF-Connecting-IP": "198.51.100.1"}); got != "172.17.0.2" {
		t.Fatalf("no trusted proxies: keyed on %q", got)
	}
}

// Behind a trusted tunnel, two visitors get their own buckets; a direct
// client cannot borrow a fresh bucket by forging the header.
func TestRateLimitBehindTrustedProxyIsPerClient(t *testing.T) {
	rl := trustingLimiter(t, "172.16.0.0/12")
	next := &nextCounter{}
	send := func(remote, cf string) int {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		r.Header.Set("CF-Connecting-IP", cf)
		w := httptest.NewRecorder()
		if err := rl.ServeHTTP(w, r, next); err != nil {
			t.Fatal(err)
		}
		return w.Code
	}
	if send("172.17.0.2:1", "198.51.100.1") != http.StatusOK || send("172.17.0.2:2", "198.51.100.2") != http.StatusOK {
		t.Fatal("distinct visitors behind the tunnel share a bucket")
	}
	if send("172.17.0.2:3", "198.51.100.1") != http.StatusTooManyRequests {
		t.Fatal("a visitor behind the tunnel escaped its bucket")
	}
	if send("203.0.113.7:1", "198.51.100.3") != http.StatusOK || send("203.0.113.7:1", "198.51.100.4") != http.StatusTooManyRequests {
		t.Fatal("an untrusted client dodged the limit with a forged header")
	}
}

func TestParseTrustedProxiesAndProvision(t *testing.T) {
	valid, bad := ParseTrustedProxies([]string{" 172.17.0.1/16 ", "", "10.0.0.1", "::ffff:192.0.2.1", "2400:cb00::/32", "nope", "10.0.0.0/33"})
	var got []string
	for _, p := range valid {
		got = append(got, p.String())
	}
	want := []string{"172.17.0.0/16", "10.0.0.1/32", "192.0.2.1/32", "2400:cb00::/32"}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(bad, []string{"nope", "10.0.0.0/33"}) {
		t.Fatalf("valid %v bad %v", got, bad)
	}

	rl := &RateLimit{RPS: 1, TrustedProxies: []string{"nope"}}
	if err := rl.Provision(caddy.Context{}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("Provision with a bad proxy = %v", err)
	}
	rl = &RateLimit{RPS: 1, TrustedProxies: []string{"172.16.0.0/12"}}
	if err := rl.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rl.Cleanup() }()
	if len(rl.trusted) != 1 || !rl.isTrusted(netipMust(t, "172.31.255.255")) {
		t.Fatalf("trusted = %v", rl.trusted)
	}
}

func TestBuildConfigPassesTrustedProxies(t *testing.T) {
	raw, warnings, err := BuildConfig(nil, nil, Options{RateLimitRPS: 50, RateLimitBurst: 100,
		TrustedProxies: []string{"172.16.0.0/12", "bogus"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `INGRESS_TRUSTED_PROXIES: ignoring "bogus"`) {
		t.Fatalf("warnings = %v", warnings)
	}
	if !strings.Contains(string(raw), `"trusted_proxies":["172.16.0.0/12"]`) {
		t.Fatalf("config lacks the trusted proxies: %s", raw)
	}
	raw, _, _ = BuildConfig(nil, nil, Options{RateLimitRPS: 50, RateLimitBurst: 100})
	if strings.Contains(string(raw), "trusted_proxies") {
		t.Fatal("trusted_proxies emitted without any configured")
	}
}
