package caddy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/ingress/domains/certtest"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func testManager(t *testing.T, ingressEnabled bool) (*Manager, string) {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("AGENT_DATA_DIR", dataDir)
	t.Setenv("INGRESS_ENABLED", map[bool]string{true: "true", false: "false"}[ingressEnabled])
	t.Setenv("LOG_LEVEL", "error")
	return NewManager(config.NewServerConfig("standalone"), logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})), dataDir
}

func TestStartSeedsDomainsFromExistingAppsOnce(t *testing.T) {
	m, dataDir := testManager(t, false) // seeding does not need the proxy
	writeApp(t, dataDir, "app-1", `{"name":"Blog","ingress":{
		"domains":[{"domain":"blog.example.com","upstream_port":1,"ssl":true},{"domain":"plain.example.com","upstream_port":2,"ssl":false}],
		"redirects":[{"domain":"www.example.com","to":"https://blog.example.com","code":301,"ssl":true},
		             {"domain":"blog.example.com","path":"/old/*","to":"https://blog.example.com/new","code":302}]}}`)
	// A later app repeating a host does not change its mode: first app wins.
	writeApp(t, dataDir, "app-2", `{"name":"Dup","ingress":{"domains":[{"domain":"blog.example.com","upstream_port":3,"ssl":false}]}}`)
	writeApp(t, dataDir, "app-3", `{"name":"Broken"`)
	writeApp(t, dataDir, "app-4", `{"name":"No ingress"}`)

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := m.ListDomains(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]command.DomainTLS{}
	for _, d := range got {
		modes[d.Name] = d.TLS
	}
	want := map[string]command.DomainTLS{
		"blog.example.com":  command.DomainTLSAuto,
		"plain.example.com": command.DomainTLSOff,
		"www.example.com":   command.DomainTLSAuto,
	}
	if len(modes) != len(want) {
		t.Fatalf("seeded = %v, want %v", modes, want)
	}
	for n, mode := range want {
		if modes[n] != mode {
			t.Errorf("%s = %q, want %q", n, modes[n], mode)
		}
	}

	// Second start: the dir exists, a new app's host is not auto-registered.
	writeApp(t, dataDir, "app-5", `{"name":"New","ingress":{"domains":[{"domain":"new.example.com","upstream_port":5,"ssl":true}]}}`)
	m2 := NewManager(config.NewServerConfig("standalone"), m.log)
	if err := m2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if again, _ := m2.ListDomains(context.Background()); len(again) != 3 {
		t.Fatalf("second start changed the registrations: %+v", again)
	}
}

func TestDomainCommandsCertificateMode(t *testing.T) {
	m, dataDir := testManager(t, true)
	certDir := t.TempDir()
	t.Setenv("HUB_CERT_DIR", certDir)
	pub := certtest.AgentKey(t, certDir)
	writeApp(t, dataDir, "app-1", `{"name":"Shop","ingress":{"domains":[{"domain":"shop.twillingate.app","upstream_port":1}]}}`)
	ctx := context.Background()

	notAfter := time.Now().Add(60 * 24 * time.Hour).Truncate(time.Second)
	cert, key := certtest.Issue(t, notAfter, "*.twillingate.app")
	d, err := m.CreateDomain(ctx, command.CreateDomainRequest{
		Name: "*.twillingate.app", TLS: command.DomainTLSCertificate,
		Certificate: string(cert), Key: certtest.Encrypt(t, pub, string(key)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.TLS != command.DomainTLSCertificate || d.NotAfter == nil || !d.NotAfter.Equal(notAfter) ||
		d.Issuer != certtest.Issuer || len(d.Apps) != 1 || d.Apps[0] != "app-1" {
		t.Fatalf("created = %+v", d)
	}
	// The decrypted key is on disk, private, and never in the response.
	stored, err := os.ReadFile(filepath.Join(dataDir, "domains", "_wildcard_.twillingate.app.key"))
	if err != nil || string(stored) != string(key) {
		t.Fatalf("stored key = %q, %v", stored, err)
	}

	bad := map[string]command.CreateDomainRequest{
		"SAN does not cover": {Name: "*.other.app", TLS: command.DomainTLSCertificate,
			Certificate: string(cert), Key: certtest.Encrypt(t, pub, string(key))},
		"key not decryptable": {Name: "*.twillingate.app", TLS: command.DomainTLSCertificate,
			Certificate: string(cert), Key: "bm90IGEgY2lwaGVydGV4dA=="},
	}
	for label, req := range bad {
		if _, err := m.CreateDomain(ctx, req); err == nil {
			t.Errorf("%s: created", label)
		}
	}
	if doms, _ := m.ListDomains(ctx); len(doms) != 1 {
		t.Fatalf("a refused create was stored: %+v", doms)
	}
	// Review focus: a failed renewal keeps the working certificate.
	wrongCert, wrongKey := certtest.Issue(t, notAfter, "*.example.com")
	if _, err := m.CreateDomain(ctx, command.CreateDomainRequest{Name: "*.twillingate.app", TLS: command.DomainTLSCertificate,
		Certificate: string(wrongCert), Key: certtest.Encrypt(t, pub, string(wrongKey))}); err == nil {
		t.Fatal("renewal with a certificate for another name succeeded")
	}
	if kept, _ := os.ReadFile(filepath.Join(dataDir, "domains", "_wildcard_.twillingate.app.crt")); string(kept) != string(cert) {
		t.Fatal("a refused renewal replaced the working certificate")
	}

	// In use: refused, naming the app.
	err = m.DeleteDomain(ctx, "*.twillingate.app")
	if err == nil || !strings.Contains(err.Error(), "Shop (app-1)") {
		t.Fatalf("delete in use = %v", err)
	}
	// An exact registration now wins shop.twillingate.app, so the wildcard
	// is free to go — and the exact one is the one in use.
	if _, err := m.CreateDomain(ctx, command.CreateDomainRequest{Name: "shop.twillingate.app", TLS: command.DomainTLSOff}); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteDomain(ctx, "*.twillingate.app"); err != nil {
		t.Fatalf("delete unused wildcard: %v", err)
	}
	if err := m.DeleteDomain(ctx, "*.twillingate.app"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("delete missing = %v", err)
	}
}

func TestDomainMutationsNeedIngress(t *testing.T) {
	m, _ := testManager(t, false)
	ctx := context.Background()
	if _, err := m.CreateDomain(ctx, command.CreateDomainRequest{Name: "x.app", TLS: command.DomainTLSOff}); !errors.Is(err, ErrIngressDisabled) {
		t.Fatalf("create = %v", err)
	}
	if err := m.DeleteDomain(ctx, "x.app"); !errors.Is(err, ErrIngressDisabled) {
		t.Fatalf("delete = %v", err)
	}
	if doms, err := m.ListDomains(ctx); err != nil || len(doms) != 0 {
		t.Fatalf("list = %+v, %v", doms, err)
	}
}
