package metric

import (
	"context"
	"testing"
	"time"

	"winterflow/internal/domain/metrics"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org1, org2 = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2 = dbtest.ID(11), dbtest.ID(12)
	appA, appB = dbtest.ID(21), dbtest.ID(22)
	appGone    = dbtest.ID(29) // never in apps
)

// t0 is a bucket-aligned instant for every tier (00:00 UTC).
var t0 = time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	dbtest.SeedApp(t, d, appA, srv1, "a")
	dbtest.SeedApp(t, d, appB, srv1, "b")
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func count(t *testing.T, d *db.DB, where string, args ...any) string {
	t.Helper()
	return dbtest.QueryString(t, d, "SELECT CAST(COUNT(*) AS TEXT) FROM metric_samples WHERE "+where, args...)
}

func mustInsert(t *testing.T, ctx context.Context, r *Repo, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values) {
	t.Helper()
	if err := r.InsertSample(ctx, serverID, at, server, apps); err != nil {
		t.Fatal(err)
	}
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestInsertSampleWritesServerAndKnownApps(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	at := t0.Add(5*time.Minute + 42*time.Second)
	mustInsert(t, ctx, repo, srv1, at, metrics.Values{"cpu": 12.5, "mem_used": 1e9},
		map[string]metrics.Values{appA: {"cpu": 3}, appGone: {"cpu": 9}})
	// The same minute again (replay, second API replica) is ignored.
	mustInsert(t, ctx, repo, srv1, at.Add(10*time.Second), metrics.Values{"cpu": 99}, nil)

	if got := count(t, d, "tier = 1"); got != "2" {
		t.Fatalf("rows = %s, want server + appA (unknown app skipped)", got)
	}
	got, err := repo.Series(ctx, srv1, "", metrics.Tier1, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Data["cpu"] != 12.5 || got[0].Samples != 1 ||
		!got[0].Start.Equal(t0.Add(5*time.Minute)) || got[0].Start.Location() != time.UTC {
		t.Fatalf("server series = %+v", got)
	}
	apps, err := repo.AppsSeries(ctx, srv1, metrics.Tier1, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || len(apps[appA]) != 1 || apps[appA][0].Data["cpu"] != 3 {
		t.Fatalf("apps series = %+v", apps)
	}
}

func TestSeriesFiltersByTargetTierAndTime(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	for i := 0; i < 5; i++ {
		mustInsert(t, ctx, repo, srv1, t0.Add(time.Duration(i)*time.Minute), metrics.Values{"cpu": float64(i)},
			map[string]metrics.Values{appA: {"cpu": float64(10 + i)}})
	}
	mustInsert(t, ctx, repo, srv2, t0, metrics.Values{"cpu": 77}, nil)

	got, err := repo.Series(ctx, srv1, "", metrics.Tier1, t0.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Data["cpu"] != 2 || got[2].Data["cpu"] != 4 {
		t.Fatalf("series = %+v", got)
	}
	app, err := repo.Series(ctx, srv1, appA, metrics.Tier1, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(app) != 5 || app[0].Data["cpu"] != 10 {
		t.Fatalf("app series = %+v", app)
	}
	if other, _ := repo.Series(ctx, srv2, "", metrics.Tier10, t0); len(other) != 0 {
		t.Fatalf("tier 10 before any rollup = %+v", other)
	}
	if empty, _ := repo.Series(ctx, srv1, appB, metrics.Tier1, t0); empty == nil || len(empty) != 0 {
		t.Fatalf("empty series = %#v, want non-nil empty", empty)
	}
}

func TestSeriesSkipsUndecodableRows(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.Exec(t, d, `INSERT INTO metric_samples (server_id, app_id, tier, bucket_start, samples, data) VALUES (?, '', 1, ?, 1, 'not json')`,
		srv1, "2026-10-11 00:00:00")
	mustInsert(t, ctx, repo, srv1, t0.Add(time.Minute), metrics.Values{"cpu": 1}, nil)
	got, err := repo.Series(ctx, srv1, "", metrics.Tier1, t0)
	if err != nil || len(got) != 1 {
		t.Fatalf("series = %+v, %v", got, err)
	}
}

func TestPruneKeepsEachTiersRetention(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	now := t0.Add(40 * 24 * time.Hour)
	for _, tier := range metrics.Tiers {
		for _, age := range []time.Duration{tier.Retention() + tier.Duration(), tier.Retention() - tier.Duration()} {
			dbtest.Exec(t, d, `INSERT INTO metric_samples (server_id, app_id, tier, bucket_start, samples, data) VALUES (?, '', ?, ?, 1, '{}')`,
				srv1, int(tier), tier.BucketStart(now.Add(-age)).Format("2006-01-02 15:04:05"))
		}
	}
	n, err := repo.Prune(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(metrics.Tiers)) {
		t.Fatalf("pruned %d rows, want one per tier", n)
	}
	for _, tier := range metrics.Tiers {
		if got := count(t, d, "tier = ?", int(tier)); got != "1" {
			t.Fatalf("tier %d keeps %s rows, want the young one", tier, got)
		}
	}
}

func TestServerDeleteCascades(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustInsert(t, ctx, repo, srv2, t0, metrics.Values{"cpu": 1}, nil)
	dbtest.Exec(t, d, `DELETE FROM servers WHERE server_id = ?`, srv2)
	if got := count(t, d, "server_id = ?", srv2); got != "0" {
		t.Fatalf("%s rows survive their server", got)
	}
}

func TestInsertSampleSkipsAppsWithoutValues(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustInsert(t, ctx, repo, srv1, t0, metrics.Values{"cpu": 1},
		map[string]metrics.Values{appA: {}, appB: {"cpu": 2}})
	if got := count(t, d, "app_id = ?", appA); got != "0" {
		t.Fatalf("app without values has %s rows", got)
	}
	if got := count(t, d, "app_id = ?", appB); got != "1" {
		t.Fatalf("app with values has %s rows", got)
	}
	if got := count(t, d, "app_id = ''"); got != "1" {
		t.Fatalf("server rows = %s", got)
	}
}

func TestInsertSampleSkipsEmptyServerRow(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustInsert(t, ctx, repo, srv1, t0, nil, map[string]metrics.Values{appA: {"cpu": 3}})
	mustInsert(t, ctx, repo, srv1, t0.Add(time.Minute), metrics.Values{}, nil)
	if got := count(t, d, "app_id = ''"); got != "0" {
		t.Fatalf("server rows = %s, want none for an empty value map", got)
	}
	if got := count(t, d, "tier = 1"); got != "1" {
		t.Fatalf("rows = %s, want only appA", got)
	}
}

func TestTier1RangeReturnsAllTargets(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustInsert(t, ctx, repo, srv1, t0.Add(time.Minute), metrics.Values{"cpu": 1}, map[string]metrics.Values{appA: {"cpu": 2}})
	mustInsert(t, ctx, repo, srv2, t0, metrics.Values{"cpu": 3}, nil)
	mustInsert(t, ctx, repo, srv1, t0.Add(-2*time.Hour), metrics.Values{"cpu": 9}, nil) // before the range
	got, err := repo.Tier1Range(ctx, t0.Add(-time.Hour), t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		server, app string
		cpu         float64
	}{{srv1, "", 1}, {srv1, appA, 2}, {srv2, "", 3}}
	if len(got) != len(want) {
		t.Fatalf("rows = %+v", got)
	}
	for i, w := range want {
		if got[i].ServerID != w.server || got[i].AppID != w.app || got[i].Data["cpu"] != w.cpu {
			t.Errorf("row %d = %+v, want %+v", i, got[i], w)
		}
	}
}
