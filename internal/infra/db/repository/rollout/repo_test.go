package rollout

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/dbtest"
)

var org1, org2 = dbtest.ID(1), dbtest.ID(2)

func newRepo(t *testing.T) *Repo {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedOrg(t, d, org1)
	dbtest.SeedOrg(t, d, org2)
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func rollout(n int, scopeKey string) model.Rollout {
	return model.Rollout{
		ID: dbtest.ID(100 + n), OrgID: org1, Kind: model.RolloutTemplateUpgrade,
		Scope: []byte(`{"template_id":"t","revision":2}`), ScopeKey: scopeKey, CreatedBy: "u1",
	}
}

func mustInsert(t *testing.T, r *Repo, ro model.Rollout) {
	t.Helper()
	if ok, err := r.InsertRollout(dbtest.Ctx(t), ro); err != nil || !ok {
		t.Fatalf("insert %s: %v %v", ro.ID, ok, err)
	}
}

func mustTrue(t *testing.T, what string, ok bool, err error) {
	t.Helper()
	if err != nil || !ok {
		t.Fatalf("%s: %v %v", what, ok, err)
	}
}

func mustFalse(t *testing.T, what string, ok bool, err error) {
	t.Helper()
	if err != nil || ok {
		t.Fatalf("%s: want false, got %v %v", what, ok, err)
	}
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestOneActiveRolloutPerScope(t *testing.T) {
	r := newRepo(t)
	ctx := dbtest.Ctx(t)
	now := time.Now().UTC().Truncate(time.Second)
	mustInsert(t, r, rollout(1, "tpl-a"))
	ok, err := r.InsertRollout(ctx, rollout(2, "tpl-a"))
	mustFalse(t, "a second active rollout of the scope", ok, err)
	mustInsert(t, r, rollout(3, "tpl-b"))

	got, err := r.GetActiveRollout(ctx, model.RolloutTemplateUpgrade, "tpl-a")
	if err != nil || got.ID != dbtest.ID(101) || got.State != model.RolloutQueued || got.ScopeRev != 1 ||
		string(got.Scope) != `{"template_id":"t","revision":2}` || len(got.SkipIDs) != 0 || got.CreatedBy != "u1" {
		t.Fatalf("active = %+v, %v", got, err)
	}
	if _, err := r.GetActiveRollout(ctx, model.RolloutVarsReload, "tpl-a"); !errors.Is(err, model.ErrRolloutNotFound) {
		t.Fatalf("another kind's scope: %v", err)
	}
	if _, err := r.GetRollout(ctx, dbtest.ID(999)); !errors.Is(err, model.ErrRolloutNotFound) {
		t.Fatalf("unknown id: %v", err)
	}

	// Once it ends, the scope takes a new rollout.
	ok, err = r.Claim(ctx, dbtest.ID(101), "w1", now, now.Add(2*time.Minute))
	mustTrue(t, "claim", ok, err)
	ok, err = r.Finish(ctx, dbtest.ID(101), "w1", model.RolloutStopped, "", "", "")
	mustTrue(t, "finish", ok, err)
	mustInsert(t, r, rollout(2, "tpl-a"))
}

func TestClaimLeaseAndTakeover(t *testing.T) {
	r := newRepo(t)
	ctx := dbtest.Ctx(t)
	now := time.Now().UTC().Truncate(time.Second)
	id := dbtest.ID(101)
	mustInsert(t, r, rollout(1, "tpl-a"))

	ids, err := r.ListClaimable(ctx, now)
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("claimable = %v, %v", ids, err)
	}
	ok, err := r.Claim(ctx, id, "w1", now, now.Add(2*time.Minute))
	mustTrue(t, "claim", ok, err)
	ok, err = r.Claim(ctx, id, "w2", now.Add(time.Minute), now.Add(3*time.Minute))
	mustFalse(t, "a live lease", ok, err)
	if ids, _ := r.ListClaimable(ctx, now.Add(time.Minute)); len(ids) != 0 {
		t.Fatalf("a leased rollout is claimable: %v", ids)
	}
	ok, err = r.RenewLease(ctx, id, "w1", now.Add(4*time.Minute))
	mustTrue(t, "renew", ok, err)
	ok, err = r.Claim(ctx, id, "w2", now.Add(3*time.Minute), now.Add(5*time.Minute))
	mustFalse(t, "a renewed lease", ok, err)

	// Past the lease another worker takes over; the first one's writes
	// then change nothing.
	ok, err = r.Claim(ctx, id, "w2", now.Add(5*time.Minute), now.Add(7*time.Minute))
	mustTrue(t, "takeover", ok, err)
	ok, err = r.RenewLease(ctx, id, "w1", now.Add(9*time.Minute))
	mustFalse(t, "the old owner's renewal", ok, err)
	ok, err = r.StartStep(ctx, id, "w1", "app", "srv", 3)
	mustFalse(t, "the old owner's step", ok, err)
	got, _ := r.GetRollout(ctx, id)
	if got.State != model.RolloutRunning || got.LeaseOwner != "w2" || got.LeaseUntil == nil ||
		!got.LeaseUntil.Equal(now.Add(7*time.Minute)) || got.CurrentAppID != "" {
		t.Fatalf("after takeover = %+v", got)
	}
}

func TestReleaseLeaseAndExtendAfterStop(t *testing.T) {
	r := newRepo(t)
	ctx := dbtest.Ctx(t)
	now := time.Now().UTC().Truncate(time.Second)
	id := dbtest.ID(101)
	mustInsert(t, r, rollout(1, "tpl-a"))
	ok, err := r.Claim(ctx, id, "w1", now, now.Add(2*time.Minute))
	mustTrue(t, "claim", ok, err)

	// Only the owner gives the lease back; the rollout stays active and is
	// claimable at once.
	ok, err = r.ReleaseLease(ctx, id, "w2", time.Time{})
	mustFalse(t, "another owner's release", ok, err)
	ok, err = r.ReleaseLease(ctx, id, "w1", time.Time{})
	mustTrue(t, "release", ok, err)
	got, _ := r.GetRollout(ctx, id)
	if got.State != model.RolloutRunning || got.LeaseOwner != "" || got.LeaseUntil != nil {
		t.Fatalf("after release = %+v", got)
	}
	if ids, _ := r.ListClaimable(ctx, now); len(ids) != 1 || ids[0] != id {
		t.Fatalf("claimable = %v", ids)
	}
	ok, err = r.RenewLease(ctx, id, "w1", now.Add(time.Hour))
	mustFalse(t, "renewal after release", ok, err)

	// A stop request closes the scope to extensions.
	ok, err = r.RequestStop(ctx, got.OrgID, id)
	mustTrue(t, "stop", ok, err)
	ok, err = r.ExtendRollout(ctx, id, 1, []byte(`{"names":["A"]}`))
	mustFalse(t, "extend a stop-requested rollout", ok, err)
	if got, _ = r.GetRollout(ctx, id); got.ScopeRev != 1 {
		t.Fatalf("scope moved: %+v", got)
	}
}

func TestParkAndStopUnleased(t *testing.T) {
	r := newRepo(t)
	ctx := dbtest.Ctx(t)
	now := time.Now().UTC().Truncate(time.Second)
	id := dbtest.ID(101)
	mustInsert(t, r, rollout(1, "tpl-a"))
	org := func() string { g, _ := r.GetRollout(ctx, id); return g.OrgID }()

	// Parked: unowned, skipped by the claimable list until the park ends
	// (a lease ending at now counts as ended).
	ok, err := r.Claim(ctx, id, "w1", now, now.Add(2*time.Minute))
	mustTrue(t, "claim", ok, err)
	ok, err = r.ReleaseLease(ctx, id, "w1", now.Add(5*time.Second))
	mustTrue(t, "park", ok, err)
	if ids, _ := r.ListClaimable(ctx, now.Add(4*time.Second)); len(ids) != 0 {
		t.Fatalf("a parked rollout is claimable: %v", ids)
	}
	if ids, _ := r.ListClaimable(ctx, now.Add(5*time.Second)); len(ids) != 1 {
		t.Fatalf("a rollout whose park ended is not claimable: %v", ids)
	}
	// StopUnleased ends a parked rollout (the org's only), not a leased one.
	ok, err = r.StopUnleased(ctx, dbtest.ID(999), id, now)
	mustFalse(t, "another org's stop", ok, err)
	ok, err = r.Claim(ctx, id, "w1", now.Add(5*time.Second), now.Add(2*time.Minute))
	mustTrue(t, "claim after the park", ok, err)
	ok, err = r.StopUnleased(ctx, org, id, now.Add(6*time.Second))
	mustFalse(t, "stop under a live lease", ok, err)
	ok, err = r.ReleaseLease(ctx, id, "w1", now.Add(time.Hour))
	mustTrue(t, "park again", ok, err)
	ok, err = r.StopUnleased(ctx, org, id, now.Add(6*time.Second))
	mustTrue(t, "stop a parked rollout", ok, err)
	if stopped, _ := r.GetRollout(ctx, id); stopped.State != model.RolloutStopped || !stopped.StopRequested || stopped.FinishedAt == nil || stopped.LeaseOwner != "" {
		t.Fatalf("stopped = %+v", stopped)
	}
}

func TestStepsExtendAndFinish(t *testing.T) {
	r := newRepo(t)
	ctx := dbtest.Ctx(t)
	now := time.Now().UTC().Truncate(time.Second)
	id := dbtest.ID(101)
	mustInsert(t, r, rollout(1, "tpl-a"))
	ok, err := r.Claim(ctx, id, "w1", now, now.Add(2*time.Minute))
	mustTrue(t, "claim", ok, err)

	ok, err = r.StartStep(ctx, id, "w1", "app-1", "srv-1", 4)
	mustTrue(t, "start", ok, err)
	ok, err = r.CountStep(ctx, id, "w1", 1, 0, nil)
	mustTrue(t, "count done", ok, err)
	ok, err = r.CountStep(ctx, id, "w1", 0, 1, []string{"inst-2"})
	mustTrue(t, "count skipped", ok, err)
	got, _ := r.GetRollout(ctx, id)
	if got.Done != 1 || got.Skipped != 1 || got.Total != 4 || len(got.SkipIDs) != 1 || got.SkipIDs[0] != "inst-2" ||
		got.CurrentAppID != "app-1" || got.CurrentServerID != "srv-1" {
		t.Fatalf("after steps = %+v", got)
	}

	// An extension moves scope_rev: completing at the old one does nothing.
	ok, err = r.ExtendRollout(ctx, id, 1, []byte(`{"names":["A"]}`))
	mustTrue(t, "extend", ok, err)
	ok, err = r.ExtendRollout(ctx, id, 1, []byte(`{"names":["B"]}`))
	mustFalse(t, "extend at a stale rev", ok, err)
	ok, err = r.Complete(ctx, id, "w1", 1)
	mustFalse(t, "complete at a stale rev", ok, err)
	got, _ = r.GetRollout(ctx, id)
	if got.ScopeRev != 2 || string(got.Scope) != `{"names":["A"]}` || got.State != model.RolloutRunning {
		t.Fatalf("after extend = %+v", got)
	}
	ok, err = r.Complete(ctx, id, "w1", 2)
	mustTrue(t, "complete", ok, err)
	got, _ = r.GetRollout(ctx, id)
	if got.State != model.RolloutDone || got.CurrentAppID != "" || got.LeaseOwner != "" || got.LeaseUntil != nil || got.FinishedAt == nil {
		t.Fatalf("done = %+v", got)
	}
	ok, err = r.ExtendRollout(ctx, id, 2, []byte(`{}`))
	mustFalse(t, "extend an ended rollout", ok, err)

	// A failure keeps the app that failed.
	mustInsert(t, r, rollout(2, "tpl-a"))
	id2 := dbtest.ID(102)
	ok, err = r.Claim(ctx, id2, "w1", now, now.Add(2*time.Minute))
	mustTrue(t, "claim 2", ok, err)
	ok, err = r.Finish(ctx, id2, "w1", model.RolloutFailed, "compose up failed", "app-9", "srv-9")
	mustTrue(t, "fail", ok, err)
	got, _ = r.GetRollout(ctx, id2)
	if got.State != model.RolloutFailed || got.Error != "compose up failed" || got.CurrentAppID != "app-9" || got.CurrentServerID != "srv-9" {
		t.Fatalf("failed = %+v", got)
	}
}

func TestRequestStopIsScopedToTheOrg(t *testing.T) {
	r := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustInsert(t, r, rollout(1, "tpl-a"))
	ok, err := r.RequestStop(ctx, org2, dbtest.ID(101))
	mustFalse(t, "another org's stop", ok, err)
	ok, err = r.RequestStop(ctx, org1, dbtest.ID(101))
	mustTrue(t, "stop", ok, err)
	if got, _ := r.GetRollout(ctx, dbtest.ID(101)); !got.StopRequested {
		t.Fatalf("stop not recorded: %+v", got)
	}
}

func TestListAndPrune(t *testing.T) {
	r := newRepo(t)
	ctx := dbtest.Ctx(t)
	now := time.Now().UTC().Truncate(time.Second)
	for n := 1; n <= 23; n++ {
		ro := rollout(n, "tpl-a")
		mustInsert(t, r, ro)
		ok, err := r.Claim(ctx, ro.ID, "w1", now, now.Add(2*time.Minute))
		mustTrue(t, fmt.Sprint("claim ", n), ok, err)
		ok, err = r.Complete(ctx, ro.ID, "w1", 1)
		mustTrue(t, fmt.Sprint("complete ", n), ok, err)
	}
	mustInsert(t, r, rollout(30, "tpl-a")) // active
	mustInsert(t, r, rollout(31, "tpl-b"))
	other := rollout(32, "tpl-c")
	other.OrgID = org2
	mustInsert(t, r, other)

	if err := r.Prune(ctx, model.RolloutTemplateUpgrade, "tpl-a", 20); err != nil {
		t.Fatal(err)
	}
	list, err := r.ListRollouts(ctx, org1, model.RolloutTemplateUpgrade, "tpl-a")
	if err != nil || len(list) != 21 || list[0].ID != dbtest.ID(130) || list[0].State != model.RolloutQueued {
		t.Fatalf("scope list = %d %v (first %+v)", len(list), err, list[0])
	}
	for _, ro := range list {
		if ro.ID == dbtest.ID(101) || ro.ID == dbtest.ID(102) || ro.ID == dbtest.ID(103) {
			t.Fatalf("pruned rollout %s still listed", ro.ID)
		}
	}
	all, err := r.ListRollouts(ctx, org1, model.RolloutTemplateUpgrade, "")
	if err != nil || len(all) != 22 || all[0].State != model.RolloutQueued || all[1].State != model.RolloutQueued {
		t.Fatalf("kind list = %d %v", len(all), err)
	}
	if got, _ := r.ListRollouts(ctx, org2, model.RolloutTemplateUpgrade, ""); len(got) != 1 {
		t.Fatalf("org2 list = %+v", got)
	}
}
