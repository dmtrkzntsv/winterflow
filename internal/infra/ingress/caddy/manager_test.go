package caddy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/ingress/domains"
	"winterflow/internal/infra/ingress/domains/certtest"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// freePort grabs an ephemeral port. ssl:false only — no ACME in tests.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// writeApp writes a minimal committed app dir the scanner reads.
func writeApp(t *testing.T, dataDir, appID, configJSON string) {
	t.Helper()
	dir := filepath.Join(dataDir, "apps-data", appID, ".winterflow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}
}

// registerDomain stores a registration the way domain.create does.
func registerDomain(t *testing.T, dataDir, name string, mode model.DomainTLS) {
	t.Helper()
	if _, err := domains.NewStore(filepath.Join(dataDir, "domains")).Put(name, mode, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func getVia(t *testing.T, port int, host, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	req.Host = host
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var resp *http.Response
	var err error
	for i := 0; i < 50; i++ { // caddy start is fast but async-ish; retry briefly
		resp, err = client.Do(req)
		if err == nil {
			return resp
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("request via caddy never succeeded: %v", err)
	return nil
}

func TestManagerServesReloadsAndIsolates(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello-from-app")
	}))
	defer upstream.Close()
	upstreamPort := upstream.Listener.Addr().(*net.TCPAddr).Port

	dataDir := t.TempDir()
	httpPort := freePort(t)
	httpsPort := freePort(t)
	t.Setenv("AGENT_DATA_DIR", dataDir)
	t.Setenv("INGRESS_ENABLED", "true")
	t.Setenv("INGRESS_HTTP_PORT", fmt.Sprint(httpPort))
	t.Setenv("INGRESS_HTTPS_PORT", fmt.Sprint(httpsPort))
	t.Setenv("LOG_LEVEL", "error")

	appCfg := func(port int) string {
		ing := map[string]any{
			"name": "t",
			"ingress": map[string]any{
				"domains": []map[string]any{{"domain": "app.test", "upstream_port": port, "ssl": false}},
				"redirects": []map[string]any{
					{"domain": "old.test", "to": "http://app.test/landed", "code": 301, "ssl": false},
					{"domain": "app.test", "path": "/legacy/*", "to": "http://app.test/new", "code": 302},
				},
			},
		}
		raw, _ := json.Marshal(ing)
		return string(raw)
	}
	writeApp(t, dataDir, "app-1", appCfg(upstreamPort))
	registerDomain(t, dataDir, "*.test", model.DomainTLSOff)
	// A second app with a broken config must not affect app-1.
	writeApp(t, dataDir, "app-2", `{"name":"broken","ingress":{"domains":[{"domain":"BAD HOST","upstream_port":1}]}}`)

	cfg := config.NewServerConfig("standalone")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	m := NewManager(cfg, log)
	t.Cleanup(m.Stop) // stop caddy before the next test touches its global state
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !m.Enabled() {
		t.Fatal("manager not enabled after successful start")
	}

	// Proxying.
	resp := getVia(t, httpPort, "app.test", "/")
	body := make([]byte, 32)
	n, _ := resp.Body.Read(body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body[:n]), "hello-from-app") {
		t.Fatalf("proxy: status %d body %q", resp.StatusCode, body[:n])
	}

	// Domain-level redirect preserves the URI.
	resp = getVia(t, httpPort, "old.test", "/some/path")
	resp.Body.Close()
	if resp.StatusCode != 301 || resp.Header.Get("Location") != "http://app.test/landed/some/path" {
		t.Fatalf("domain redirect: %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Path rule redirects to the exact target and wins over the proxy route.
	resp = getVia(t, httpPort, "app.test", "/legacy/x")
	resp.Body.Close()
	if resp.StatusCode != 302 || resp.Header.Get("Location") != "http://app.test/new" {
		t.Fatalf("path rule: %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Hot reload: change the app's domain, Reload, old gone / new serving.
	newCfg := strings.ReplaceAll(appCfg(upstreamPort), "app.test", "app2.test")
	writeApp(t, dataDir, "app-1", newCfg)
	if warnings := m.Reload(ctx); len(warnings) != 1 { // app-2's bad fragment still warns
		t.Fatalf("reload warnings = %v, want exactly the app-2 exclusion", warnings)
	}
	resp = getVia(t, httpPort, "app2.test", "/")
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("reloaded domain not serving: %d", resp.StatusCode)
	}
}

func TestManagerDisabledOnBindFailure(t *testing.T) {
	// Occupy the HTTP port so caddy cannot bind it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	taken := l.Addr().(*net.TCPAddr).Port

	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	t.Setenv("INGRESS_ENABLED", "true")
	t.Setenv("INGRESS_HTTP_PORT", fmt.Sprint(taken))
	t.Setenv("INGRESS_HTTPS_PORT", fmt.Sprint(freePort(t)))
	t.Setenv("LOG_LEVEL", "error")

	cfg := config.NewServerConfig("standalone")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	m := NewManager(cfg, log)
	t.Cleanup(m.Stop) // stop caddy before the next test touches its global state
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatalf("bind failure must degrade, not error: %v", err)
	}
	if m.Enabled() {
		t.Fatal("manager claims enabled despite bind failure")
	}
	if w := m.Reload(ctx); w != nil {
		t.Fatalf("disabled manager Reload must no-op, got %v", w)
	}
}

func TestManagerRespectsDisableSwitch(t *testing.T) {
	// A free port proves the switch, not a bind failure, is what stops caddy:
	// were it ignored, the manager would come up enabled here.
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	t.Setenv("INGRESS_ENABLED", "false")
	t.Setenv("INGRESS_HTTP_PORT", fmt.Sprint(freePort(t)))
	t.Setenv("INGRESS_HTTPS_PORT", fmt.Sprint(freePort(t)))
	t.Setenv("LOG_LEVEL", "error")

	cfg := config.NewServerConfig("standalone")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	m := NewManager(cfg, log)
	t.Cleanup(m.Stop) // stop caddy before the next test touches its global state
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatalf("disabled ingress must not error: %v", err)
	}
	if m.Enabled() {
		t.Fatal("INGRESS_ENABLED=false but manager reports enabled")
	}
}

func TestManagerRefusesPortCollisionWithAPI(t *testing.T) {
	shared := freePort(t)
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	t.Setenv("INGRESS_ENABLED", "true")
	t.Setenv("API_PORT", fmt.Sprint(shared))
	t.Setenv("INGRESS_HTTP_PORT", fmt.Sprint(shared))
	t.Setenv("INGRESS_HTTPS_PORT", fmt.Sprint(freePort(t)))
	t.Setenv("LOG_LEVEL", "error")

	cfg := config.NewServerConfig("standalone")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	m := NewManager(cfg, log)
	t.Cleanup(m.Stop) // stop caddy before the next test touches its global state
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatalf("collision must degrade, not error: %v", err)
	}
	if m.Enabled() {
		t.Fatal("ingress started on the API's port")
	}

	// The same ports with API_PORT unset are fine — the guard must not fire
	// on a lone agent, which never runs the API.
	unsetAPIPort(t)
	m2 := NewManager(config.NewServerConfig("standalone"), log)
	t.Cleanup(m2.Stop)
	if err := m2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !m2.Enabled() {
		t.Fatal("ingress refused to start with API_PORT unset")
	}
}

// unsetAPIPort removes API_PORT for the test's duration (t.Setenv registers
// the restore, then Unsetenv makes it genuinely absent rather than empty).
func unsetAPIPort(t *testing.T) {
	t.Helper()
	t.Setenv("API_PORT", "")
	_ = os.Unsetenv("API_PORT")
}

// TestManagerServesUploadedCertificateAndCatchAll runs real Caddy with a
// certificate-mode wildcard and an off-mode wildcard: HTTPS for any host
// under the wildcard presents the uploaded certificate (no ACME involved),
// and hosts no app claims answer the catch-all 404 on both servers.
func TestManagerServesUploadedCertificateAndCatchAll(t *testing.T) {
	dataDir := t.TempDir()
	httpPort, httpsPort := freePort(t), freePort(t)
	t.Setenv("AGENT_DATA_DIR", dataDir)
	t.Setenv("INGRESS_ENABLED", "true")
	t.Setenv("INGRESS_HTTP_PORT", fmt.Sprint(httpPort))
	t.Setenv("INGRESS_HTTPS_PORT", fmt.Sprint(httpsPort))
	t.Setenv("LOG_LEVEL", "error")

	cert, key := certtest.Issue(t, time.Now().Add(90*24*time.Hour), "*.cert.test")
	store := domains.NewStore(filepath.Join(dataDir, "domains"))
	if _, err := store.Put("*.cert.test", model.DomainTLSCertificate, cert, key); err != nil {
		t.Fatal(err)
	}
	registerDomain(t, dataDir, "*.tunnel.test", model.DomainTLSOff)

	m := NewManager(config.NewServerConfig("standalone"), logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
	t.Cleanup(m.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Start(ctx); err != nil || !m.Enabled() {
		t.Fatalf("start: %v, enabled %v", err, m.Enabled())
	}

	// Off mode: plain HTTP, unknown host under the wildcard is a clean 404.
	resp := getVia(t, httpPort, "nope.tunnel.test", "/")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "no app is served on this hostname") {
		t.Fatalf("off catch-all: %d %q", resp.StatusCode, body)
	}

	// Certificate mode: the TLS handshake presents the uploaded leaf.
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		ServerName:         "nope.cert.test",
		InsecureSkipVerify: true, // the test CA is not in the system pool; the leaf is checked below
	}}}
	req, _ := http.NewRequest("GET", fmt.Sprintf("https://127.0.0.1:%d/", httpsPort), nil)
	req.Host = "nope.cert.test"
	var tlsResp *http.Response
	var err error
	for i := 0; i < 50; i++ {
		if tlsResp, err = client.Do(req); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("https via caddy: %v", err)
	}
	defer tlsResp.Body.Close()
	leaf := tlsResp.TLS.PeerCertificates[0]
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "*.cert.test" || leaf.Issuer.CommonName != certtest.Issuer {
		t.Fatalf("served certificate = %v issued by %q", leaf.DNSNames, leaf.Issuer.CommonName)
	}
	if tlsResp.StatusCode != http.StatusNotFound {
		t.Fatalf("certificate catch-all: %d", tlsResp.StatusCode)
	}
}
