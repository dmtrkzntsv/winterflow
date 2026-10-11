package serverstorage

import (
	"context"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org1, org2 = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2 = dbtest.ID(11), dbtest.ID(12)
)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func summary(name string, typ model.StorageType, loc string) model.StorageSummary {
	return model.StorageSummary{Name: name, Type: typ, Location: loc}
}

func mustUpsert(t *testing.T, ctx context.Context, r *Repo, serverID string, s model.StorageSummary) {
	t.Helper()
	if err := r.Upsert(ctx, serverID, s); err != nil {
		t.Fatal(err)
	}
}

func mustList(t *testing.T, ctx context.Context, r *Repo, serverID string) []model.StorageSummary {
	t.Helper()
	got, err := r.ListForServer(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestServerStoragesUpsertListDelete(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustUpsert(t, ctx, repo, srv1, summary("hetzner", model.StorageS3, "https://s3/b/"))
	// Editing updates the row in place.
	mustUpsert(t, ctx, repo, srv1, summary("hetzner", model.StorageS3, "https://s3/b/tenants/"))
	mustUpsert(t, ctx, repo, srv1, summary("disk", model.StorageLocal, "/mnt/usb/winterflow"))
	// The same name on another server is its own row.
	mustUpsert(t, ctx, repo, srv2, summary("hetzner", model.StorageLocal, "/mnt/x"))

	got := mustList(t, ctx, repo, srv1)
	if len(got) != 2 || got[0] != summary("disk", model.StorageLocal, "/mnt/usb/winterflow") ||
		got[1] != summary("hetzner", model.StorageS3, "https://s3/b/tenants/") {
		t.Fatalf("list = %+v", got)
	}
	if err := repo.Delete(ctx, srv1, "disk"); err != nil {
		t.Fatal(err)
	}
	if got := mustList(t, ctx, repo, srv1); len(got) != 1 {
		t.Fatalf("after delete: %+v", got)
	}
	if got := mustList(t, ctx, repo, srv2); len(got) != 1 || got[0].Type != model.StorageLocal {
		t.Fatalf("other server touched: %+v", got)
	}
	// Deleting what is not there is silent.
	if err := repo.Delete(ctx, srv1, "missing"); err != nil {
		t.Fatal(err)
	}
}

func TestServerStoragesListEmptyIsNonNil(t *testing.T) {
	repo, _ := newRepo(t)
	if got := mustList(t, dbtest.Ctx(t), repo, srv1); got == nil || len(got) != 0 {
		t.Fatalf("empty list = %#v, want non-nil empty slice", got)
	}
}

// The location column holds the longest location the model allows.
func TestServerStoragesLongLocation(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	loc := "/" + strings.Repeat("a", 1023)
	mustUpsert(t, ctx, repo, srv1, summary("long", model.StorageLocal, loc))
	if got := mustList(t, ctx, repo, srv1); len(got) != 1 || got[0].Location != loc {
		t.Fatalf("long location not round-tripped: %d rows", len(got))
	}
}

func TestServerStoragesReplaceForServer(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustUpsert(t, ctx, repo, srv1, summary("old", model.StorageLocal, "/mnt/old"))
	mustUpsert(t, ctx, repo, srv2, summary("keep", model.StorageLocal, "/mnt/keep"))

	if err := repo.ReplaceForServer(ctx, srv1, []model.StorageSummary{
		summary("b", model.StorageS3, "https://s3/b/"), summary("a", model.StorageLocal, "/mnt/a"),
	}); err != nil {
		t.Fatal(err)
	}
	got := mustList(t, ctx, repo, srv1)
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("after replace: %+v", got)
	}
	if err := repo.ReplaceForServer(ctx, srv1, nil); err != nil {
		t.Fatal(err)
	}
	if got := mustList(t, ctx, repo, srv1); len(got) != 0 {
		t.Fatalf("empty replace left %+v", got)
	}
	if got := mustList(t, ctx, repo, srv2); len(got) != 1 {
		t.Fatalf("other server touched: %+v", got)
	}
}

// A failing insert rolls the whole replace back: the old rows survive.
func TestServerStoragesReplaceIsAtomic(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustUpsert(t, ctx, repo, srv1, summary("old", model.StorageLocal, "/mnt/old"))
	dup := []model.StorageSummary{summary("x", model.StorageLocal, "/mnt/x"), summary("x", model.StorageS3, "https://s3/x/")}
	if err := repo.ReplaceForServer(ctx, srv1, dup); err == nil {
		t.Fatal("duplicate names accepted")
	}
	if got := mustList(t, ctx, repo, srv1); len(got) != 1 || got[0].Name != "old" {
		t.Fatalf("after failed replace: %+v", got)
	}
}

// TestServerStoragesCascadeWithServer: removing a server drops its index.
func TestServerStoragesCascadeWithServer(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustUpsert(t, ctx, repo, srv1, summary("a", model.StorageLocal, "/mnt/a"))
	dbtest.Exec(t, d, `DELETE FROM servers WHERE server_id = ?`, srv1)
	if got := mustList(t, ctx, repo, srv1); len(got) != 0 {
		t.Fatalf("rows survived their server: %+v", got)
	}
}

// TestServerStoragesReplaceSkipsUnchanged: an agent reconnect re-reports the
// same storages; that must not rewrite the rows.
func TestServerStoragesReplaceSkipsUnchanged(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	rows := []model.StorageSummary{summary("a", model.StorageLocal, "/mnt/a"), summary("b", model.StorageS3, "https://s3/b/")}
	if err := repo.ReplaceForServer(ctx, srv1, rows); err != nil {
		t.Fatal(err)
	}
	stamps := func() map[string]string {
		out := map[string]string{}
		for _, name := range []string{"a", "b"} {
			out[name] = dbtest.QueryString(t, d, `SELECT CAST(updated_at AS TEXT) FROM server_storages WHERE server_id = ? AND name = ?`, srv1, name)
		}
		return out
	}
	before := stamps()
	// Force the clock past the stored second, then re-report in another order.
	time.Sleep(1100 * time.Millisecond)
	if err := repo.ReplaceForServer(ctx, srv1, []model.StorageSummary{rows[1], rows[0]}); err != nil {
		t.Fatal(err)
	}
	after := stamps()
	for name, at := range before {
		if after[name] != at {
			t.Errorf("%s rewritten although unchanged: %v -> %v", name, at, after[name])
		}
	}
	// A changed location is written.
	if err := repo.ReplaceForServer(ctx, srv1, []model.StorageSummary{summary("a", model.StorageLocal, "/mnt/z")}); err != nil {
		t.Fatal(err)
	}
	if got := mustList(t, ctx, repo, srv1); len(got) != 1 || got[0].Location != "/mnt/z" {
		t.Fatalf("changed list not written: %+v", got)
	}
}

// Timestamps keep the Bun-era storage format: naive UTC
// "2006-01-02 15:04:05" text (TIMESTAMP on both engines here).
func TestServerStoragesStoredTimestampFormat(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustUpsert(t, ctx, repo, srv1, summary("a", model.StorageLocal, "/mnt/a"))
	got := dbtest.QueryString(t, d, `SELECT CAST(updated_at AS TEXT) FROM server_storages WHERE name = 'a'`)
	if _, err := time.Parse("2006-01-02 15:04:05", got); err != nil {
		t.Fatalf("stored updated_at = %q, want 2006-01-02 15:04:05 layout: %v", got, err)
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
