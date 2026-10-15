package bootstrap

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	feed "winterflow/internal/domain/catalog"
	"winterflow/internal/domain/model"
	catalogsync "winterflow/internal/domain/usecase/catalog"
	"winterflow/internal/infra/db"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// catalogDeps is WireCore over a fresh SQLite database.
func catalogDeps(t *testing.T) (*Deps, context.Context) {
	t.Helper()
	t.Setenv("DATABASE_URL", "sqlite://"+filepath.Join(t.TempDir(), "wf.sqlite"))
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	conn, err := db.Open(log, cfg.GetDbURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Shutdown() })
	deps, _ := WireCore(ctx, membus.NewBus(log), conn, cfg, log)
	return deps, ctx
}

// waitCatalog waits until cond holds for the catalog templates.
func waitCatalog(t *testing.T, d *Deps, ctx context.Context, cond func([]model.CatalogState) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		list, err := d.CatalogRepository.ListCatalog(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if cond(list) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("catalog = %+v", list)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTheDefaultCatalogKeyParses(t *testing.T) {
	if _, err := feed.ParsePublicKey(config.DefaultCatalogPublicKey); err != nil {
		t.Fatal(err)
	}
}

// StartCatalog's sync fetches CATALOG_URL, verifies it with
// CATALOG_PUBLIC_KEY and fills the catalog.
func TestStartCatalogSyncs(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	files := map[string]string{"compose.yml": "services:\n  app:\n    image: example/notes:1\n"}
	raw, err := feed.Encode(feed.Feed{Format: feed.FormatVersion, GeneratedAt: time.Now().UTC().Truncate(time.Second), Apps: []feed.Entry{
		{Slug: "notes", Name: "Notes", Files: files, Digest: feed.Digest(files)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog.json":
			_, _ = w.Write(raw)
		case "/catalog.json.sig":
			_, _ = w.Write(feed.Sign(key, raw))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("CATALOG_URL", srv.URL+"/catalog.json")
	t.Setenv("CATALOG_PUBLIC_KEY", base64.StdEncoding.EncodeToString(pub))
	d, ctx := catalogDeps(t)
	if s := startCatalog(ctx, d, catalogsync.Schedule{First: time.Millisecond}); s == nil {
		t.Fatal("the sync did not start")
	}
	waitCatalog(t, d, ctx, func(l []model.CatalogState) bool { return len(l) == 1 && l[0].Slug == "notes" })
}

// CATALOG_URL empty withdraws every catalog template once; a URL or key
// that can't be used leaves the sync off.
func TestStartCatalogOff(t *testing.T) {
	t.Setenv("CATALOG_URL", "")
	d, ctx := catalogDeps(t)
	if err := d.TemplateRepository.CreateTemplate(ctx, model.Template{ID: "00000000-0000-7000-8000-000000000001", Source: model.TemplateSourceCatalog, Slug: "notes", Name: "Notes"},
		model.TemplateRevision{Body: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	StartCatalog(ctx, d)
	waitCatalog(t, d, ctx, func(l []model.CatalogState) bool { return len(l) == 1 && l[0].WithdrawnAt != nil })

	for name, env := range map[string][2]string{
		"an http URL": {"http://winterflow.io/catalog/catalog.json", ""},
		"a bad key":   {config.DefaultCatalogURL, "not a key"},
		"a short key": {config.DefaultCatalogURL, base64.StdEncoding.EncodeToString([]byte("short"))},
		"not a URL":   {"https://[bad", ""},
	} {
		t.Setenv("CATALOG_URL", env[0])
		t.Setenv("CATALOG_PUBLIC_KEY", env[1])
		if s := startCatalog(ctx, d, catalogsync.Schedule{}); s != nil {
			t.Errorf("%s: the sync started", name)
		}
	}
}
