package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org1, org2 = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2 = dbtest.ID(11), dbtest.ID(12)
	appA, appB = dbtest.ID(21), dbtest.ID(22) // appA < appB < appC
	appC       = dbtest.ID(23)
	tmpl       = dbtest.ID(90)
)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	// Apps reference a server row; seed one.
	dbtest.SeedServer(t, d, org1, srv1)
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func mustGet(t *testing.T, ctx context.Context, r *Repo, id string) model.App {
	t.Helper()
	got, err := r.GetApp(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustList(t *testing.T, ctx context.Context, r *Repo, serverID string) []model.App {
	t.Helper()
	got, err := r.GetApps(ctx, serverID)
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

func TestAppCRUDRoundTrip(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)

	a := model.App{ID: appA, ServerID: srv1, Name: "grafana", Version: "1", Icon: "chart", Color: "#123456", TemplateID: tmpl}
	if err := repo.SaveApp(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetApp(ctx, appA)
	if err != nil || got.Name != "grafana" || got.ServerID != srv1 || got.TemplateID != tmpl || got.CreatedAt.IsZero() {
		t.Fatalf("GetApp = %+v, %v", got, err)
	}

	// Upsert on conflict: same id, new name, template cleared.
	a.Name, a.TemplateID = "grafana-2", ""
	if err := repo.SaveApp(ctx, a); err != nil {
		t.Fatal(err)
	}
	got = mustGet(t, ctx, repo, appA)
	if got.Name != "grafana-2" || got.TemplateID != "" {
		t.Fatalf("upsert = %+v", got)
	}

	if err := repo.RenameApp(ctx, appA, "renamed"); err != nil {
		t.Fatal(err)
	}
	got = mustGet(t, ctx, repo, appA)
	if got.Name != "renamed" {
		t.Fatalf("renamed = %q", got.Name)
	}

	list, err := repo.GetApps(ctx, srv1)
	if err != nil || len(list) != 1 {
		t.Fatalf("GetApps = %+v, %v", list, err)
	}

	if err := repo.DeleteApp(ctx, appA); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetApp(ctx, appA); !errors.Is(err, model.ErrAppNotFound) {
		t.Fatalf("after delete err = %v", err)
	}
}

// The upsert keeps the stored server_id and created_at: only name,
// version, icon, color and template_id change.
func TestSaveAppKeepsServerAndCreatedAt(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedServer(t, d, org2, srv2)
	first := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := repo.SaveApp(ctx, model.App{ID: appA, ServerID: srv1, Name: "a", CreatedAt: first}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveApp(ctx, model.App{ID: appA, ServerID: srv2, Name: "b", Version: "2", Icon: "x", Color: "#ffffff", CreatedAt: first.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	got := mustGet(t, ctx, repo, appA)
	if got.ServerID != srv1 || !got.CreatedAt.Equal(first) || got.Name != "b" || got.Version != "2" || got.Icon != "x" || got.Color != "#ffffff" {
		t.Fatalf("after second save = %+v", got)
	}
}

func TestSyncAppsMirrorsAgentState(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedServer(t, d, org2, srv2)
	for _, a := range []model.App{
		{ID: appA, ServerID: srv1, Name: "keep"},
		{ID: appB, ServerID: srv1, Name: "drop"},
		{ID: appC, ServerID: srv2, Name: "other server"},
	} {
		if err := repo.SaveApp(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	newApp := dbtest.ID(24)
	// Agent reports: keep (renamed) + a new app; "drop" is gone.
	if err := repo.SyncApps(ctx, srv1, []model.App{
		{ID: appA, Name: "keep-updated"},
		{ID: newApp, Name: "brand-new"},
	}); err != nil {
		t.Fatal(err)
	}
	list, err := repo.GetApps(ctx, srv1)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, a := range list {
		names[a.ID] = a.Name
	}
	if len(list) != 2 || names[appA] != "keep-updated" || names[newApp] != "brand-new" {
		t.Fatalf("after sync: %v", names)
	}
	if other := mustList(t, ctx, repo, srv2); len(other) != 1 {
		t.Fatalf("another server's apps touched: %+v", other)
	}
}

func TestSyncAppsEmptyReportDeletesAll(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	if err := repo.SaveApp(ctx, model.App{ID: appA, ServerID: srv1, Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SyncApps(ctx, srv1, nil); err != nil {
		t.Fatal(err)
	}
	if list := mustList(t, ctx, repo, srv1); len(list) != 0 {
		t.Fatalf("want empty after empty sync, got %v", list)
	}
}

// GetApps breaks created_at ties (second precision) by app id, so both
// databases list apps saved in the same second in the same order.
func TestGetAppsOrdersTiesByID(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{appB, appA, appC} {
		if err := repo.SaveApp(ctx, model.App{ID: id, ServerID: srv1, Name: id, Icon: "i", Color: "#000000", CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := repo.GetApps(ctx, srv1)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range list {
		got = append(got, a.ID)
	}
	if strings.Join(got, ",") != strings.Join([]string{appA, appB, appC}, ",") {
		t.Fatalf("order = %v", got)
	}
}

// created_at is stored as naive UTC "2006-01-02 15:04:05" text on SQLite
// (the Bun-era format) and read back as the same instant in UTC.
func TestCreatedAtStorage(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	at := time.Date(2026, 7, 2, 15, 4, 5, 0, time.FixedZone("UTC+3", 3*3600))
	if err := repo.SaveApp(ctx, model.App{ID: appA, ServerID: srv1, Name: "a", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if d.Driver == backend.SQLite {
		if raw := dbtest.QueryString(t, d, `SELECT CAST(created_at AS TEXT) FROM apps`); raw != "2026-07-02 12:04:05" {
			t.Fatalf("stored created_at = %q", raw)
		}
	}
	got := mustGet(t, ctx, repo, appA)
	if !got.CreatedAt.Equal(at) || got.CreatedAt.Location() != time.UTC {
		t.Fatalf("read back %v (%v)", got.CreatedAt, got.CreatedAt.Location())
	}
}

// seedSample writes one tier-1 metric row straight into metric_samples.
func seedSample(t *testing.T, d *db.DB, serverID, appID string) {
	t.Helper()
	dbtest.Exec(t, d, `INSERT INTO metric_samples (server_id, app_id, tier, bucket_start, samples, data) VALUES (?, ?, 1, ?, 1, '{}')`,
		serverID, appID, "2026-10-11 00:00:00")
}

func samples(t *testing.T, d *db.DB, appID string) string {
	t.Helper()
	return dbtest.QueryString(t, d, `SELECT CAST(COUNT(*) AS TEXT) FROM metric_samples WHERE app_id = ?`, appID)
}

func TestDeleteAppRemovesItsMetricSamples(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	for _, id := range []string{appA, appB} {
		if err := repo.SaveApp(ctx, model.App{ID: id, ServerID: srv1, Name: id}); err != nil {
			t.Fatal(err)
		}
		seedSample(t, d, srv1, id)
	}
	seedSample(t, d, srv1, "") // the server's own row

	if err := repo.DeleteApp(ctx, appA); err != nil {
		t.Fatal(err)
	}
	if got := samples(t, d, appA); got != "0" {
		t.Fatalf("deleted app keeps %s samples", got)
	}
	if samples(t, d, appB) != "1" || samples(t, d, "") != "1" {
		t.Fatal("other targets' samples touched")
	}

	// A reconcile that no longer lists appB drops its samples too.
	if err := repo.SyncApps(ctx, srv1, nil); err != nil {
		t.Fatal(err)
	}
	if got := samples(t, d, appB); got != "0" {
		t.Fatalf("reconciled-away app keeps %s samples", got)
	}
}

// An empty app id addresses no app: it must not reach the samples delete,
// where app_id = '' matches every server's own rows.
func TestDeleteAppWithEmptyIDKeepsServerSamples(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	seedSample(t, d, srv1, "")
	if err := repo.DeleteApp(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got := samples(t, d, ""); got != "1" {
		t.Fatalf("server samples after DeleteApp(\"\") = %s, want 1", got)
	}
}
