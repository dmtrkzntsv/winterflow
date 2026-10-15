package caddy

import (
	"strings"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/ingress/domains"
)

// An expose target proxies to the host port allocated for it; one without
// an allocation is skipped with a warning, never routed to port 0.
func TestBuildConfigResolvesExposeTargets(t *testing.T) {
	apps := []AppIngress{{
		AppID: "notes",
		Ingress: model.Ingress{Domains: []model.IngressDomain{
			{Domain: "notes.example.com", Expose: "app:8080", SSL: true},
			{Domain: "admin.example.com", Expose: "admin:9000", SSL: true},
		}},
		Ports: map[string]int{"app:8080": 20013},
	}}
	raw, warnings, err := BuildConfig(apps, []domains.Registration{reg("*.example.com", model.DomainTLSAuto)}, opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "app notes: domain admin.example.com: admin:9000 has no host port yet, skipping") {
		t.Fatalf("warnings = %v", warnings)
	}
	cfg := string(raw)
	if !strings.Contains(cfg, `"dial":"127.0.0.1:20013"`) || strings.Contains(cfg, `127.0.0.1:0"`) {
		t.Fatalf("config = %s", cfg)
	}
}

// The manager reads each app's allocations from the same config.json.
func TestReadAppsCarriesHTTPPorts(t *testing.T) {
	m, dataDir := testManager(t, false)
	writeApp(t, dataDir, "notes", `{"name":"Notes","ingress":{"domains":[{"domain":"notes.example.com","expose":"app:8080","ssl":true}]},"ports":{"app:8080/http":{"host":20013,"pinned":false},"db:5432/tcp":{"host":20014,"pinned":false}}}`)
	writeApp(t, dataDir, "broken", `{"ingress":{"domains":[]},"ports":{"x:1/http":{"host":0}}}`)
	apps, warnings, err := m.readApps()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]AppIngress{}
	for _, a := range apps {
		byID[a.AppID] = a
	}
	if p := byID["notes"].Ports; len(p) != 1 || p["app:8080"] != 20013 {
		t.Fatalf("ports = %v", p)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "app broken: unreadable ports") {
		t.Fatalf("warnings = %v", warnings)
	}
}
