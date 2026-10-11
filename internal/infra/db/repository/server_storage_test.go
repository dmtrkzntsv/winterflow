package repository

import (
	"context"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/models"
	"winterflow/pkg/logger"
)

func newServerStorageRepo(t *testing.T) *DbServerStorageRepository {
	t.Helper()
	conn := newTestDB(t)
	seedOrgWithServer(t, conn, "org-1", "srv-1")
	seedOrgWithServer(t, conn, "org-2", "srv-2")
	return NewDbServerStorageRepository(conn, logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
}

func summary(name string, typ model.StorageType, loc string) model.StorageSummary {
	return model.StorageSummary{Name: name, Type: typ, Location: loc}
}

func TestServerStoragesUpsertListDelete(t *testing.T) {
	repo := newServerStorageRepo(t)
	ctx := context.Background()
	if err := repo.Upsert(ctx, "srv-1", summary("hetzner", model.StorageS3, "https://s3/b/")); err != nil {
		t.Fatal(err)
	}
	// Editing updates the row in place.
	if err := repo.Upsert(ctx, "srv-1", summary("hetzner", model.StorageS3, "https://s3/b/tenants/")); err != nil {
		t.Fatal(err)
	}
	if err := repo.Upsert(ctx, "srv-1", summary("disk", model.StorageLocal, "/mnt/usb")); err != nil {
		t.Fatal(err)
	}
	// The same name on another server is its own row.
	if err := repo.Upsert(ctx, "srv-2", summary("hetzner", model.StorageLocal, "/mnt/x")); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ListForServer(ctx, "srv-1")
	if err != nil || len(got) != 2 || got[0] != summary("disk", model.StorageLocal, "/mnt/usb") ||
		got[1] != summary("hetzner", model.StorageS3, "https://s3/b/tenants/") {
		t.Fatalf("list = %+v, %v", got, err)
	}
	if err := repo.Delete(ctx, "srv-1", "disk"); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, "srv-1"); len(got) != 1 {
		t.Fatalf("after delete: %+v", got)
	}
	if got, _ := repo.ListForServer(ctx, "srv-2"); len(got) != 1 || got[0].Type != model.StorageLocal {
		t.Fatalf("other server touched: %+v", got)
	}
}

func TestServerStoragesReplaceForServer(t *testing.T) {
	repo := newServerStorageRepo(t)
	ctx := context.Background()
	_ = repo.Upsert(ctx, "srv-1", summary("old", model.StorageLocal, "/mnt/old"))
	_ = repo.Upsert(ctx, "srv-2", summary("keep", model.StorageLocal, "/mnt/keep"))
	rows := []model.StorageSummary{summary("b", model.StorageS3, "https://s3/b/"), summary("a", model.StorageLocal, "/mnt/a")}
	if err := repo.ReplaceForServer(ctx, "srv-1", rows); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, "srv-1"); len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("after replace: %+v", got)
	}

	// An unchanged answer (another order) writes nothing.
	stamp := func() time.Time {
		var row models.ServerStorage
		if err := repo.db.GetDB().NewSelect().Model(&row).Where("server_id = ? AND name = ?", "srv-1", "a").Scan(ctx); err != nil {
			t.Fatal(err)
		}
		return row.UpdatedAt.Time()
	}
	before := stamp()
	time.Sleep(1100 * time.Millisecond)
	if err := repo.ReplaceForServer(ctx, "srv-1", []model.StorageSummary{rows[1], rows[0]}); err != nil {
		t.Fatal(err)
	}
	if after := stamp(); !after.Equal(before) {
		t.Fatalf("rewritten although unchanged: %v -> %v", before, after)
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

// TestServerStoragesCascadeWithServer: removing a server drops its index.
func TestServerStoragesCascadeWithServer(t *testing.T) {
	repo := newServerStorageRepo(t)
	ctx := context.Background()
	_ = repo.Upsert(ctx, "srv-1", summary("a", model.StorageLocal, "/mnt/a"))
	if _, err := repo.db.GetDB().NewDelete().Model((*models.Server)(nil)).Where("server_id = ?", "srv-1").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, "srv-1"); len(got) != 0 {
		t.Fatalf("rows survived their server: %+v", got)
	}
}

func TestSameStorages(t *testing.T) {
	base := []model.StorageSummary{summary("a", model.StorageLocal, "/mnt/a"), summary("b", model.StorageS3, "https://s3/b/")}
	cases := map[string]struct {
		other []model.StorageSummary
		same  bool
	}{
		"reordered": {[]model.StorageSummary{base[1], base[0]}, true},
		"fewer":     {base[:1], false},
		"renamed":   {[]model.StorageSummary{base[0], summary("c", model.StorageS3, "https://s3/b/")}, false},
		"type":      {[]model.StorageSummary{summary("a", model.StorageS3, "/mnt/a"), base[1]}, false},
		"location":  {[]model.StorageSummary{summary("a", model.StorageLocal, "/mnt/z"), base[1]}, false},
	}
	for name, tc := range cases {
		if got := sameStorages(base, tc.other); got != tc.same {
			t.Errorf("%s: sameStorages = %v, want %v", name, got, tc.same)
		}
	}
}
