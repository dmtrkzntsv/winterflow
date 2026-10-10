package repository

import (
	"context"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/models"
	"winterflow/pkg/logger"
)

func newServerDomainRepo(t *testing.T) *DbServerDomainRepository {
	t.Helper()
	conn := newTestDB(t)
	seedOrgWithServer(t, conn, "org-1", "srv-1")
	seedOrgWithServer(t, conn, "org-2", "srv-2")
	return NewDbServerDomainRepository(conn, logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
}

func TestServerDomainsUpsertListDelete(t *testing.T) {
	repo := newServerDomainRepo(t)
	ctx := context.Background()
	notAfter := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)

	if err := repo.Upsert(ctx, "srv-1", model.RegisteredDomain{Name: "*.twillingate.app", TLS: "auto"}); err != nil {
		t.Fatal(err)
	}
	// Replacing the certificate updates the row in place.
	if err := repo.Upsert(ctx, "srv-1", model.RegisteredDomain{Name: "*.twillingate.app", TLS: "certificate", NotAfter: &notAfter, Issuer: "CF Origin"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Upsert(ctx, "srv-1", model.RegisteredDomain{Name: "twillingate.app", TLS: "off"}); err != nil {
		t.Fatal(err)
	}
	// The same name on another server is its own row.
	if err := repo.Upsert(ctx, "srv-2", model.RegisteredDomain{Name: "*.twillingate.app", TLS: "off"}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ListForServer(ctx, "srv-1")
	if err != nil || len(got) != 2 {
		t.Fatalf("list = %+v, %v", got, err)
	}
	if got[0].Name != "*.twillingate.app" || got[0].TLS != "certificate" || got[0].Issuer != "CF Origin" ||
		got[0].NotAfter == nil || !got[0].NotAfter.Equal(notAfter) {
		t.Errorf("certificate row = %+v", got[0])
	}
	if got[1].Name != "twillingate.app" || got[1].NotAfter != nil {
		t.Errorf("off row = %+v", got[1])
	}

	if err := repo.Delete(ctx, "srv-1", "twillingate.app"); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, "srv-1"); len(got) != 1 {
		t.Fatalf("after delete: %+v", got)
	}
	if got, _ := repo.ListForServer(ctx, "srv-2"); len(got) != 1 || got[0].TLS != "off" {
		t.Fatalf("other server touched: %+v", got)
	}
}

func TestServerDomainsReplaceForServer(t *testing.T) {
	repo := newServerDomainRepo(t)
	ctx := context.Background()
	_ = repo.Upsert(ctx, "srv-1", model.RegisteredDomain{Name: "old.app", TLS: "auto"})
	_ = repo.Upsert(ctx, "srv-2", model.RegisteredDomain{Name: "keep.app", TLS: "auto"})

	if err := repo.ReplaceForServer(ctx, "srv-1", []model.RegisteredDomain{
		{Name: "a.app", TLS: "auto"}, {Name: "*.b.app", TLS: "off"},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.ListForServer(ctx, "srv-1")
	if len(got) != 2 || got[0].Name != "*.b.app" || got[1].Name != "a.app" {
		t.Fatalf("after replace: %+v", got)
	}
	if err := repo.ReplaceForServer(ctx, "srv-1", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, "srv-1"); len(got) != 0 {
		t.Fatalf("empty replace left %+v", got)
	}
	if got, _ := repo.ListForServer(ctx, "srv-2"); len(got) != 1 {
		t.Fatalf("other server touched: %+v", got)
	}
}

// TestServerDomainsCascadeWithServer: removing a server drops its index.
func TestServerDomainsCascadeWithServer(t *testing.T) {
	repo := newServerDomainRepo(t)
	ctx := context.Background()
	_ = repo.Upsert(ctx, "srv-1", model.RegisteredDomain{Name: "a.app", TLS: "auto"})
	if _, err := repo.db.GetDB().NewDelete().Model((*models.Server)(nil)).Where("server_id = ?", "srv-1").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, "srv-1"); len(got) != 0 {
		t.Fatalf("rows survived their server: %+v", got)
	}
}
