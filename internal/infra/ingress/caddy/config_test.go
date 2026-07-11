package caddy

import (
	"encoding/json"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
)

func opts() Options {
	return Options{HTTPPort: 80, HTTPSPort: 443, ACMEEmail: "ops@example.com", StorageDir: "/data/caddy", LogLevel: "info"}
}

// dig walks a decoded JSON tree: dig(t, cfg, "apps", "http", "servers").
func dig(t *testing.T, v any, path ...string) any {
	t.Helper()
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok || m[p] == nil {
			t.Fatalf("missing %q in %v", strings.Join(path, "."), v)
		}
		v = m[p]
	}
	return v
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("config is not valid JSON: %v\n%s", err, raw)
	}
	return m
}

func sampleApps() []AppIngress {
	return []AppIngress{
		{AppID: "app-b", Ingress: model.Ingress{
			Domains: []model.IngressDomain{{Domain: "plain.example.com", UpstreamPort: 9000, SSL: false}},
		}},
		{AppID: "app-a", Ingress: model.Ingress{
			Domains: []model.IngressDomain{{Domain: "blog.example.com", UpstreamPort: 8088, SSL: true}},
			Redirects: []model.IngressRedirect{
				{Domain: "www.example.com", To: "https://blog.example.com", Code: 301, SSL: true},
				{Domain: "blog.example.com", Path: "/old/*", To: "https://blog.example.com/new", Code: 302},
			},
		}},
	}
}

func TestBuildConfigShape(t *testing.T) {
	raw, warnings, err := BuildConfig(sampleApps(), opts())
	if err != nil || len(warnings) != 0 {
		t.Fatalf("BuildConfig: %v, warnings %v", err, warnings)
	}
	cfg := decode(t, raw)

	if dig(t, cfg, "admin", "disabled") != true {
		t.Error("admin API not disabled")
	}
	if dig(t, cfg, "logging", "logs", "default", "level") != "INFO" {
		t.Error("log level not mapped")
	}
	if dig(t, cfg, "storage", "root") != "/data/caddy" {
		t.Error("storage root missing")
	}

	// TLS automation: exactly the ssl hostnames.
	subjects := dig(t, cfg, "apps", "tls", "automation", "policies").([]any)[0].(map[string]any)["subjects"].([]any)
	got := map[string]bool{}
	for _, s := range subjects {
		got[s.(string)] = true
	}
	if len(got) != 2 || !got["blog.example.com"] || !got["www.example.com"] {
		t.Errorf("subjects = %v", subjects)
	}

	s := string(raw)
	for _, want := range []string{
		`"127.0.0.1:8088"`, `"127.0.0.1:9000"`, // upstreams
		`"blog.example.com"`, `"plain.example.com"`, `"www.example.com"`,
		`https://blog.example.com{http.request.uri}`, // domain-level redirect preserves URI
		`"/old/*"`,                       // path matcher
		`"https://blog.example.com/new"`, // path rule = exact target
		`"ops@example.com"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("config missing %s\n%s", want, s)
		}
	}

	// The path rule must be ordered before its domain's main route.
	if pathIdx, routeIdx := strings.Index(s, `"/old/*"`), strings.Index(s, `"127.0.0.1:8088"`); pathIdx > routeIdx {
		t.Error("path rule ordered after its domain's route")
	}
}

func TestBuildConfigIsolatesBadFragmentsAndDups(t *testing.T) {
	apps := append(sampleApps(),
		// Invalid fragment: whole app excluded, others unaffected.
		AppIngress{AppID: "app-bad", Ingress: model.Ingress{
			Domains: []model.IngressDomain{{Domain: "NOT A HOST", UpstreamPort: 1}},
		}},
		// Duplicate across apps: sorted-appID order, first claim wins.
		AppIngress{AppID: "app-z", Ingress: model.Ingress{
			Domains: []model.IngressDomain{{Domain: "blog.example.com", UpstreamPort: 7000, SSL: true}},
		}},
	)
	raw, warnings, err := BuildConfig(apps, opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want 2 (bad fragment + dup)", warnings)
	}
	s := string(raw)
	if strings.Contains(s, "NOT A HOST") {
		t.Error("invalid fragment leaked into config")
	}
	if !strings.Contains(s, `"127.0.0.1:8088"`) || strings.Contains(s, `"127.0.0.1:7000"`) {
		t.Error("dup resolution wrong: app-a (sorted first) must win blog.example.com")
	}
}

func TestBuildConfigEmptyAndNoSSL(t *testing.T) {
	// No apps: still a valid config with empty servers (Caddy runs, serves nothing).
	raw, warnings, err := BuildConfig(nil, opts())
	if err != nil || len(warnings) != 0 {
		t.Fatalf("empty: %v %v", err, warnings)
	}
	cfg := decode(t, raw)
	if _, hasTLS := dig(t, cfg, "apps").(map[string]any)["tls"]; hasTLS {
		t.Error("tls app present with zero ssl domains")
	}

	// Debug level turns access logs on.
	o := opts()
	o.LogLevel = "debug"
	raw, _, _ = BuildConfig(sampleApps(), o)
	if !strings.Contains(string(raw), `"logs"`) {
		t.Error("debug level did not enable access logs")
	}
	raw, _, _ = BuildConfig(sampleApps(), opts())
	cfg = decode(t, raw)
	srv := dig(t, cfg, "apps", "http", "servers", "https").(map[string]any)
	if _, ok := srv["logs"]; ok {
		t.Error("access logs enabled at info level")
	}
}
