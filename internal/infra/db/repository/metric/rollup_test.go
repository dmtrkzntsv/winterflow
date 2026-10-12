package metric

import (
	"context"
	"testing"
	"time"

	"winterflow/internal/domain/metrics"
	"winterflow/internal/infra/db/dbtest"
)

// minutes inserts one tier-1 row per minute for [from, from+n).
func minutes(t *testing.T, ctx context.Context, r *Repo, from time.Time, n int, cpu func(i int) float64) {
	t.Helper()
	for i := 0; i < n; i++ {
		mustInsert(t, ctx, r, srv1, from.Add(time.Duration(i)*time.Minute), metrics.Values{"cpu": cpu(i)},
			map[string]metrics.Values{appA: {"cpu": 1}})
	}
}

func TestRollupBuildsClosedBucketsOnce(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	// 00:00-00:24: two closed 10-minute buckets and an open one.
	minutes(t, ctx, repo, t0, 25, func(i int) float64 { return float64(i) })
	now := t0.Add(25 * time.Minute)

	n, err := repo.Rollup(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	// Tier 10: buckets 00:00 and 00:10 for the server and appA (00:20 is
	// still open); tier 20: bucket 00:00-00:20 closed too, from those rows.
	if n != 6 {
		t.Fatalf("wrote %d rows, want 6", n)
	}
	got, err := repo.Series(ctx, srv1, "", metrics.Tier10, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Samples != 10 || got[0].Data["cpu"] != 4.5 || got[0].Data["cpu_max"] != 9 ||
		got[1].Data["cpu"] != 14.5 || got[1].Data["cpu_max"] != 19 {
		t.Fatalf("tier 10 = %+v", got)
	}
	// Idempotent: a second run (or a second API replica) writes nothing.
	if n, err := repo.Rollup(ctx, now); err != nil || n != 0 {
		t.Fatalf("second rollup wrote %d, %v", n, err)
	}
	if got := count(t, d, "tier = 10"); got != "4" {
		t.Fatalf("tier 10 rows = %s", got)
	}
}

func TestRollupCascadesThroughEveryTier(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	minutes(t, ctx, repo, t0, 60, func(i int) float64 {
		if i == 30 {
			return 100
		}
		return 10
	})
	// The loop runs every 10 minutes; each run only looks back over its
	// source tier's retention. At 01:02 tiers 10 and 20 are complete; at
	// 08:02 the 2-hour and 8-hour buckets starting 00:00 are closed.
	for _, now := range []time.Time{t0.Add(62 * time.Minute), t0.Add(8*time.Hour + metrics.RollupGrace)} {
		if _, err := repo.Rollup(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := repo.Series(ctx, srv1, "", metrics.Tier20, t0); len(got) != 3 || got[1].Data["cpu_max"] != 100 {
		t.Fatalf("tier 20 = %+v", got)
	}
	for _, tier := range []metrics.Tier{metrics.Tier120, metrics.Tier480} {
		got, err := repo.Series(ctx, srv1, "", tier, t0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Samples != 60 || got[0].Data["cpu_max"] != 100 {
			t.Fatalf("tier %d = %+v", tier, got)
		}
		if avg := got[0].Data["cpu"]; avg < 11.49 || avg > 11.51 { // (59*10+100)/60
			t.Fatalf("tier %d avg = %v", tier, avg)
		}
	}
}

func TestRollupSkipsDeletedApps(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	minutes(t, ctx, repo, t0, 10, func(int) float64 { return 1 })
	// The app row goes away after its samples were written but before the
	// rollup (its tier-1 rows linger until the delete path or retention).
	dbtest.Exec(t, d, `DELETE FROM apps WHERE app_id = ?`, appA)
	if _, err := repo.Rollup(ctx, t0.Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := count(t, d, "tier = 10 AND app_id = ?", appA); got != "0" {
		t.Fatalf("rolled up %s rows for a deleted app", got)
	}
	if got := count(t, d, "tier = 10 AND app_id = ''"); got != "1" {
		t.Fatalf("server rollup = %s", got)
	}
}

func TestRollupLeavesGapsAsGaps(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	// Offline from 00:10 to 00:30: no tier-1 rows, so no tier-10 rows.
	minutes(t, ctx, repo, t0, 10, func(int) float64 { return 1 })
	minutes(t, ctx, repo, t0.Add(30*time.Minute), 10, func(int) float64 { return 1 })
	if _, err := repo.Rollup(ctx, t0.Add(45*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Series(ctx, srv1, "", metrics.Tier10, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Start.Equal(t0) || !got[1].Start.Equal(t0.Add(30*time.Minute)) {
		t.Fatalf("tier 10 = %+v, want 00:00 and 00:30 only", got)
	}
}
