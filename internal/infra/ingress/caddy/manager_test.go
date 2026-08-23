package caddy

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// A second app with a broken config must not affect app-1.
	writeApp(t, dataDir, "app-2", `{"name":"broken","ingress":{"domains":[{"domain":"BAD HOST","upstream_port":1}]}}`)

	cfg := config.NewServerConfig("standalone")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	m := NewManager(cfg, log)
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
	t.Setenv("INGRESS_HTTP_PORT", fmt.Sprint(taken))
	t.Setenv("INGRESS_HTTPS_PORT", fmt.Sprint(freePort(t)))
	t.Setenv("LOG_LEVEL", "error")

	cfg := config.NewServerConfig("standalone")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	m := NewManager(cfg, log)
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
