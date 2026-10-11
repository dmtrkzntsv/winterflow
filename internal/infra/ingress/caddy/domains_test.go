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
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/ingress/domains/certtest"
	"winterflow/pkg/config"
	"winterflow/pkg/crypto/cryptotest"
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
	modes := map[string]model.DomainTLS{}
	for _, d := range got {
		modes[d.Name] = d.TLS
	}
	want := map[string]model.DomainTLS{
		"blog.example.com":  model.DomainTLSAuto,
		"plain.example.com": model.DomainTLSOff,
		"www.example.com":   model.DomainTLSAuto,
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
	pub := cryptotest.AgentKey(t, certDir)
	writeApp(t, dataDir, "app-1", `{"name":"Shop","ingress":{"domains":[{"domain":"shop.twillingate.app","upstream_port":1}]}}`)
	ctx := context.Background()

	notAfter := time.Now().Add(60 * 24 * time.Hour).Truncate(time.Second)
	cert, key := certtest.Issue(t, notAfter, "*.twillingate.app")
	d, err := m.CreateDomain(ctx, command.CreateDomainRequest{
		Name: "*.twillingate.app", TLS: model.DomainTLSCertificate,
		Certificate: string(cert), Key: cryptotest.Encrypt(t, pub, string(key)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.TLS != model.DomainTLSCertificate || d.NotAfter == nil || !d.NotAfter.Equal(notAfter) ||
		d.Issuer != certtest.Issuer || len(d.Apps) != 1 || d.Apps[0] != "app-1" {
		t.Fatalf("created = %+v", d)
	}
	// The decrypted key is on disk, private, and never in the response.
	regs, _ := m.domains.List()
	if len(regs) != 1 || !strings.HasPrefix(regs[0].KeyFile, filepath.Join(dataDir, "domains", "_wildcard_.twillingate.app.")) {
		t.Fatalf("stored = %+v", regs)
	}
	stored, err := os.ReadFile(regs[0].KeyFile)
	if err != nil || string(stored) != string(key) {
		t.Fatalf("stored key = %q, %v", stored, err)
	}
	working := regs[0].CertFile

	bad := map[string]command.CreateDomainRequest{
		"SAN does not cover": {Name: "*.other.app", TLS: model.DomainTLSCertificate,
			Certificate: string(cert), Key: cryptotest.Encrypt(t, pub, string(key))},
		"key not decryptable": {Name: "*.twillingate.app", TLS: model.DomainTLSCertificate,
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
	if _, err := m.CreateDomain(ctx, command.CreateDomainRequest{Name: "*.twillingate.app", TLS: model.DomainTLSCertificate,
		Certificate: string(wrongCert), Key: cryptotest.Encrypt(t, pub, string(wrongKey))}); err == nil {
		t.Fatal("renewal with a certificate for another name succeeded")
	}
	if regs, _ := m.domains.List(); len(regs) != 1 || regs[0].CertFile != working {
		t.Fatalf("a refused renewal replaced the working certificate: %+v", regs)
	}
	if kept, _ := os.ReadFile(working); string(kept) != string(cert) {
		t.Fatal("a refused renewal replaced the working certificate")
	}

	// In use: refused, naming the app (by name, not id).
	err = m.DeleteDomain(ctx, "*.twillingate.app")
	if err == nil || !strings.Contains(err.Error(), "used by Shop and") || strings.Contains(err.Error(), "app-1") {
		t.Fatalf("delete in use = %v", err)
	}
	// An exact registration now wins shop.twillingate.app, so the wildcard
	// is free to go — and the exact one is the one in use.
	if _, err := m.CreateDomain(ctx, command.CreateDomainRequest{Name: "shop.twillingate.app", TLS: model.DomainTLSOff}); err != nil {
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
	if _, err := m.CreateDomain(ctx, command.CreateDomainRequest{Name: "x.app", TLS: model.DomainTLSOff}); !errors.Is(err, ErrIngressDisabled) {
		t.Fatalf("create = %v", err)
	}
	if err := m.DeleteDomain(ctx, "x.app"); !errors.Is(err, ErrIngressDisabled) {
		t.Fatalf("delete = %v", err)
	}
	if doms, err := m.ListDomains(ctx); err != nil || len(doms) != 0 {
		t.Fatalf("list = %+v, %v", doms, err)
	}
}

// TestDeleteDomainRefusesOnlyOrphanedHosts is the upgrade path: a seeded
// exact name can be deleted once a wildcard covers its host too (the app
// moves to the wildcard), while the sole cover of a host stays refused.
func TestDeleteDomainRefusesOnlyOrphanedHosts(t *testing.T) {
	m, dataDir := testManager(t, true)
	writeApp(t, dataDir, "app-1", `{"name":"Shop","ingress":{"domains":[{"domain":"shop.example.com","upstream_port":1}]}}`)
	writeApp(t, dataDir, "app-2", `{"ingress":{"redirects":[{"domain":"old.example.com","to":"https://shop.example.com","code":301}]}}`)
	writeApp(t, dataDir, "app-3", `{"name":"Other","ingress":{"domains":[{"domain":"other.example.org","upstream_port":3}]}}`)
	ctx := context.Background()
	for _, name := range []string{"shop.example.com", "old.example.com", "*.example.com", "other.example.org"} {
		registerDomain(t, dataDir, name, model.DomainTLSOff)
	}

	// The exact names win their hosts, but the wildcard still covers them.
	if err := m.DeleteDomain(ctx, "shop.example.com"); err != nil {
		t.Fatalf("delete exact name a wildcard also covers: %v", err)
	}
	if err := m.DeleteDomain(ctx, "old.example.com"); err != nil {
		t.Fatalf("delete exact redirect source a wildcard also covers: %v", err)
	}
	// Now the wildcard is the only cover: refused, naming both apps.
	// An app without a name is named by its id.
	err := m.DeleteDomain(ctx, "*.example.com")
	if err == nil || !strings.Contains(err.Error(), "used by Shop, app-2 and") ||
		strings.Contains(err.Error(), "Other") {
		t.Fatalf("delete sole cover = %v", err)
	}
	// An exact sole cover is refused too.
	if err := m.DeleteDomain(ctx, "other.example.org"); err == nil || !strings.Contains(err.Error(), "used by Other and") {
		t.Fatalf("delete sole exact cover = %v", err)
	}
	doms, _ := m.ListDomains(ctx)
	if len(doms) != 2 {
		t.Fatalf("registrations = %+v, want the wildcard and other.example.org", doms)
	}
}

// TestSeedDomainsRetriesWhenAppsUnreadable: an apps-data that can't be read
// must not seed an empty domains/ (that would run once and strand every
// existing hostname); the next start seeds once it is readable.
func TestSeedDomainsRetriesWhenAppsUnreadable(t *testing.T) {
	m, dataDir := testManager(t, false)
	appsData := filepath.Join(dataDir, "apps-data")
	// A file where the directory should be: ReadDir fails with something
	// other than not-exist, whatever user runs the test.
	if err := os.WriteFile(appsData, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "domains")); !os.IsNotExist(err) {
		t.Fatalf("domains/ created from an unreadable apps-data: %v", err)
	}

	if err := os.Remove(appsData); err != nil {
		t.Fatal(err)
	}
	writeApp(t, dataDir, "app-1", `{"name":"Blog","ingress":{"domains":[{"domain":"blog.example.com","upstream_port":1,"ssl":true}]}}`)
	writeApp(t, dataDir, "app-2", `{"name":"Broken"`) // a warning, not a reason to skip
	m2 := NewManager(config.NewServerConfig("standalone"), m.log)
	if err := m2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	doms, err := m2.ListDomains(context.Background())
	if err != nil || len(doms) != 1 || doms[0].Name != "blog.example.com" || doms[0].TLS != model.DomainTLSAuto {
		t.Fatalf("seeded on retry = %+v, %v", doms, err)
	}
}

// TestDeleteDomainFailsClosed: when an app's config (or apps-data itself)
// can't be read, the delete can't tell whether the name is in use, so it is
// refused rather than stranding that app's hostnames.
func TestDeleteDomainFailsClosed(t *testing.T) {
	m, dataDir := testManager(t, true)
	ctx := context.Background()
	registerDomain(t, dataDir, "*.example.com", model.DomainTLSOff)
	writeApp(t, dataDir, "app-1", `{"name":"Broken"`)
	err := m.DeleteDomain(ctx, "*.example.com")
	if err == nil || !strings.Contains(err.Error(), "can't be checked") || !strings.Contains(err.Error(), "app-1") {
		t.Fatalf("delete with an unreadable app config = %v", err)
	}

	appsData := filepath.Join(dataDir, "apps-data")
	if err := os.RemoveAll(appsData); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appsData, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteDomain(ctx, "*.example.com"); err == nil || !strings.Contains(err.Error(), "can't be checked") {
		t.Fatalf("delete with an unreadable apps-data = %v", err)
	}
	if doms, _ := m.ListDomains(ctx); len(doms) != 1 {
		t.Fatalf("a refused delete removed the registration: %+v", doms)
	}
}

func TestCreateDomainHonoursCancellation(t *testing.T) {
	m, _ := testManager(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.CreateDomain(ctx, command.CreateDomainRequest{Name: "x.app", TLS: model.DomainTLSOff}); !errors.Is(err, context.Canceled) {
		t.Fatalf("create on a canceled context = %v", err)
	}
	if doms, _ := m.ListDomains(context.Background()); len(doms) != 0 {
		t.Fatalf("a canceled create was stored: %+v", doms)
	}
}
