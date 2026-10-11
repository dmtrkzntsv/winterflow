package caddy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	caddy "github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/ingress/domains"
	"winterflow/internal/infra/ingress/domains/certtest"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
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
// the trusted proxies — through Caddy's own validator, so a schema mistake,
// an unloadable certificate or an unregistered module fails here instead of
// at agent startup.
func TestBuildConfigValidatesWithCaddy(t *testing.T) {
	dir := t.TempDir()
	store := domains.NewStore(dir)
	cert, key := certtest.Issue(t, time.Now().Add(90*24*time.Hour), "*.cert.test")
	for _, r := range []struct {
		name      string
		mode      model.DomainTLS
		cert, key []byte
	}{
		{"*.cert.test", model.DomainTLSCertificate, cert, key},
		{"x.cert.test", model.DomainTLSOff, nil, nil},
		{"example.test", model.DomainTLSAuto, nil, nil},
		{"*.auto.test", model.DomainTLSAuto, nil, nil},
		{"*.tunnel.test", model.DomainTLSOff, nil, nil},
	} {
		if _, err := store.Put(r.name, r.mode, r.cert, r.key); err != nil {
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

// TestRateLimitKeysOnCaddysClientIP: the throttle keys on Caddy's
// client_ip var (which the server's trusted_proxies settings derive), and on
// the TCP peer when the var is absent.
func TestRateLimitKeysOnCaddysClientIP(t *testing.T) {
	withVar := httptest.NewRequest("GET", "/", nil)
	withVar.RemoteAddr = "172.17.0.2:40000"
	withVar = withVar.WithContext(context.WithValue(withVar.Context(), caddyhttp.VarsCtxKey,
		map[string]any{caddyhttp.ClientIPVarKey: "198.51.100.1"}))
	if got := clientIP(withVar); got != "198.51.100.1" {
		t.Errorf("with client_ip: keyed on %q", got)
	}
	bare := httptest.NewRequest("GET", "/", nil)
	bare.RemoteAddr = "203.0.113.7:1"
	bare.Header.Set("CF-Connecting-IP", "198.51.100.9") // never read here
	if got := clientIP(bare); got != "203.0.113.7" {
		t.Errorf("without client_ip: keyed on %q", got)
	}
	bare.RemoteAddr = "203.0.113.7"
	if got := clientIP(bare); got != "203.0.113.7" {
		t.Errorf("peer without a port: keyed on %q", got)
	}
}

func TestTrustedRanges(t *testing.T) {
	valid, bad := trustedRanges([]string{" 172.17.0.1/16 ", "", "10.0.0.1", "2400:cb00::/32", "nope", "10.0.0.0/33"})
	if want := []string{"172.17.0.0/16", "10.0.0.1/32", "2400:cb00::/32"}; !reflect.DeepEqual(valid, want) {
		t.Errorf("valid = %v, want %v", valid, want)
	}
	if want := []string{"nope", "10.0.0.0/33"}; !reflect.DeepEqual(bad, want) {
		t.Errorf("bad = %v, want %v", bad, want)
	}
}

// TestBuildConfigTrustsProxiesNatively: INGRESS_TRUSTED_PROXIES becomes
// Caddy's own trusted_proxies on both servers (strict, CF-Connecting-IP
// before X-Forwarded-For); an invalid entry is dropped with a warning, never
// trusted.
func TestBuildConfigTrustsProxiesNatively(t *testing.T) {
	raw, warnings, err := BuildConfig(nil, nil, Options{TrustedProxies: []string{"172.16.0.0/12", "bogus"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `INGRESS_TRUSTED_PROXIES: ignoring "bogus"`) {
		t.Fatalf("warnings = %v", warnings)
	}
	v := decodeView(t, raw)
	for _, name := range []string{"http", "https"} {
		srv := v.Apps.HTTP.Servers[name]
		if srv.TrustedProxies["source"] != "static" || !reflect.DeepEqual(srv.TrustedProxies["ranges"], []any{"172.16.0.0/12"}) ||
			!reflect.DeepEqual(srv.ClientIPHeaders, []string{"CF-Connecting-IP", "X-Forwarded-For"}) || srv.TrustedProxiesStrict != 1 {
			t.Errorf("server %s: trusted_proxies %v, client_ip_headers %v, strict %d",
				name, srv.TrustedProxies, srv.ClientIPHeaders, srv.TrustedProxiesStrict)
		}
	}
	for _, opts := range []Options{{}, {TrustedProxies: []string{"bogus"}}} {
		raw, _, _ = BuildConfig(nil, nil, opts)
		if strings.Contains(string(raw), "trusted_proxies") || strings.Contains(string(raw), "client_ip_headers") {
			t.Fatalf("%v: trusted proxies emitted without a valid one", opts.TrustedProxies)
		}
	}
}

// TestRateLimitBehindTrustedProxyLive runs real Caddy (requests arrive from
// 127.0.0.1): behind a trusted proxy two visitors named by CF-Connecting-IP
// get their own buckets; from an untrusted peer the header is ignored, so a
// forged one can't borrow a fresh bucket.
func TestRateLimitBehindTrustedProxyLive(t *testing.T) {
	dataDir := t.TempDir()
	httpPort := freePort(t)
	t.Setenv("AGENT_DATA_DIR", dataDir)
	t.Setenv("INGRESS_ENABLED", "true")
	t.Setenv("INGRESS_HTTP_PORT", fmt.Sprint(httpPort))
	t.Setenv("INGRESS_HTTPS_PORT", fmt.Sprint(freePort(t)))
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("INGRESS_RATE_LIMIT_RPS", "0.01") // one token per 100s: no refill mid-test
	t.Setenv("INGRESS_RATE_LIMIT_BURST", "1")
	t.Setenv("INGRESS_TRUSTED_PROXIES", "127.0.0.0/8")
	registerDomain(t, dataDir, "*.tunnel.test", model.DomainTLSOff)

	m := NewManager(config.NewServerConfig("standalone"), logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
	t.Cleanup(m.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Start(ctx); err != nil || !m.Enabled() {
		t.Fatalf("start: %v, enabled %v", err, m.Enabled())
	}
	status := func(cf string) int {
		t.Helper()
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/", httpPort), nil)
		req.Host = "x.tunnel.test"
		req.Header.Set("CF-Connecting-IP", cf)
		var resp *http.Response
		var err error
		for i := 0; i < 50; i++ {
			if resp, err = http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
				return resp.StatusCode
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("request via caddy: %v", err)
		return 0
	}

	// Trusted peer: one bucket per visitor.
	if a, b := status("198.51.100.1"), status("198.51.100.2"); a != http.StatusNotFound || b != http.StatusNotFound {
		t.Fatalf("distinct visitors behind the proxy: %d, %d", a, b)
	}
	if got := status("198.51.100.1"); got != http.StatusTooManyRequests {
		t.Fatalf("a visitor behind the proxy escaped its bucket: %d", got)
	}

	// Untrusted peer (127.0.0.1 no longer listed): the header is ignored and
	// every request shares the peer's bucket.
	t.Setenv("INGRESS_TRUSTED_PROXIES", "10.0.0.0/8")
	if warnings := m.Reload(ctx); len(warnings) != 0 {
		t.Fatalf("reload: %v", warnings)
	}
	if a, b := status("198.51.100.3"), status("198.51.100.4"); a != http.StatusNotFound || b != http.StatusTooManyRequests {
		t.Fatalf("forged headers from an untrusted peer: %d, %d (want 404, 429)", a, b)
	}
}
