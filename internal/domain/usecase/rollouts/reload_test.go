package rollouts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/dbtest"
)

// stale is a running app stale for names.
func stale(appID string, names ...string) command.AppStatus {
	return command.AppStatus{AppID: appID, StatusCode: command.ContainerStatusActive, StaleVars: names,
		Containers: []command.ContainerStatus{{StatusCode: command.ContainerStatusActive}}}
}

// reloadEnv is an env whose agent has apps on srv1 at shared-variables
// revision 3, reported once; the API's catalog knows them.
func reloadEnv(t *testing.T, apps ...command.AppStatus) (*env, *fakeAgent) {
	t.Helper()
	e := newEnv(t)
	a, _ := e.withAgent(t)
	a.mu.Lock()
	a.varsRev[srv1] = 3
	a.mu.Unlock()
	for _, st := range apps {
		dbtest.SeedApp(t, e.d, st.AppID, srv1, st.AppID)
		a.set(srv1, st)
	}
	a.report(srv1)
	return e, a
}

// smtpOnSrv1 is the scope a save of SMTP_PASS with reload makes on srv1.
func smtpOnSrv1() model.ReloadScope {
	return model.ReloadScope{Names: []string{"SMTP_PASS"}, ServerIDs: []string{srv1}, Revisions: map[string]int64{srv1: 3}}
}

// activeReload is the org's active vars_reload rollout.
func (e *env) activeReload(t *testing.T, orgID string) model.Rollout {
	t.Helper()
	r, err := e.repo.GetActiveRollout(dbtest.Ctx(t), model.RolloutVarsReload, orgID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestQueueReloadQueuesOnceThenExtends(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	first := e.activeReload(t, org1)
	more := model.ReloadScope{Names: []string{"S3_KEY"}, ServerIDs: []string{srv2}, AllStale: []string{srv2}, Revisions: map[string]int64{srv1: 4, srv2: 1}}
	if err := e.uc.QueueReload(ctx, "u2", org1, more); err != nil {
		t.Fatal(err)
	}
	got := e.activeReload(t, org1)
	s, _ := reloadScope(got)
	if got.ID != first.ID || got.ScopeRev != 2 || got.CreatedBy != "u1" || got.ScopeKey != org1 ||
		!slices.Equal(s.Names, []string{"S3_KEY", "SMTP_PASS"}) || !slices.Equal(s.Servers(), []string{srv1, srv2}) ||
		s.Revisions[srv1] != 4 || s.Revisions[srv2] != 1 || s.SettleBy.IsZero() {
		t.Fatalf("extended = %+v, scope %+v", got, s)
	}
	// Another org has its own.
	if err := e.uc.QueueReload(ctx, "u9", org2, model.ReloadScope{AllStale: []string{srv2}}); err != nil {
		t.Fatal(err)
	}
	if other := e.activeReload(t, org2); other.ID == first.ID {
		t.Fatal("org2 joined org1's rollout")
	}
}

func TestReloadRolloutTakesTheStaleRunningApps(t *testing.T) {
	stopped := stale("a3", "SMTP_PASS")
	stopped.StatusCode, stopped.Containers = command.ContainerStatusStopped, []command.ContainerStatus{{StatusCode: command.ContainerStatusStopped}}
	e, a := reloadEnv(t, stale("a1", "SMTP_PASS"), stale("a2", "OTHER"), stopped, stale("a4", "SMTP_PASS", "OTHER"))
	ctx := dbtest.Ctx(t)
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	e.uc.pass(ctx)
	r := e.get(t, e.pub.events()[0].ID)
	if r.State != model.RolloutDone || r.Done != 2 || r.Total != 2 {
		t.Fatalf("rollout = %+v", r)
	}
	// a2 is stale for another name, a3 is stopped.
	if got := a.commands(); !slices.Equal(got, []string{"vars.reload a1", "vars.reload a4"}) {
		t.Fatalf("commands = %v", got)
	}
}

func TestReloadWaitsForTheServerToReportTheChange(t *testing.T) {
	e, a := reloadEnv(t, stale("a1", "SMTP_PASS"))
	ctx := dbtest.Ctx(t)
	scope := smtpOnSrv1()
	scope.Revisions[srv1] = 4 // the save bumped it; the agent still reports 3
	if err := e.uc.QueueReload(ctx, "u1", org1, scope); err != nil {
		t.Fatal(err)
	}
	id := e.activeReload(t, org1).ID
	if !e.uc.pass(ctx) {
		t.Fatal("a rollout waiting for its server did not yield the worker")
	}
	if got := a.commands(); len(got) != 0 {
		t.Fatalf("reloaded before the server reported the change: %v", got)
	}
	// It is parked: running, unowned, its lease ending at the next poll.
	if r := e.get(t, id); r.State != model.RolloutRunning || r.LeaseOwner != "" || r.LeaseUntil == nil {
		t.Fatalf("waiting rollout = %+v", r)
	}
	a.mu.Lock()
	a.varsRev[srv1] = 4
	a.mu.Unlock()
	a.report(srv1)
	e.passUntil(t, func() bool { return e.get(t, id).State == model.RolloutDone })
	if r := e.get(t, id); r.Done != 1 {
		t.Fatalf("rollout = %+v", r)
	}

	// A server that never reports the change is left out after SettleBy.
	if err := e.uc.QueueReload(ctx, "u1", org1, model.ReloadScope{Names: []string{"X"}, ServerIDs: []string{srv2}, Revisions: map[string]int64{srv2: 9}}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	e.passUntil(t, func() bool { return e.activeOrLast(t, org1).State == model.RolloutDone })
	if r := e.activeOrLast(t, org1); r.State != model.RolloutDone || r.Done != 0 || time.Since(start) < e.uc.settle/2 {
		t.Fatalf("unsettled rollout = %+v after %s", r, time.Since(start))
	}
}

func TestAWaitingReloadYieldsTheWorkerToOtherRollouts(t *testing.T) {
	e, a := reloadEnv(t, stale("a1", "SMTP_PASS"))
	ctx := dbtest.Ctx(t)
	scope := smtpOnSrv1()
	scope.Revisions[srv1] = 4 // never reported while this test runs
	if err := e.uc.QueueReload(ctx, "u1", org1, scope); err != nil {
		t.Fatal(err)
	}
	reload := e.activeReload(t, org1).ID
	k := &fakeKind{todo: []string{"x1", "x2"}}
	e.uc.kinds["fake"] = k
	other := e.queueFake(t, 1, "s")
	// A second org's reload rides the same pass.
	if err := e.uc.QueueReload(ctx, "u9", org2, model.ReloadScope{Names: []string{"N"}, ServerIDs: []string{srv2}, Revisions: map[string]int64{srv2: 5}}); err != nil {
		t.Fatal(err)
	}
	if !e.uc.pass(ctx) {
		t.Fatal("the pass did not report the waiting rollouts")
	}
	if r := e.get(t, other); r.State != model.RolloutDone || r.Done != 2 {
		t.Fatalf("the rollout queued behind the waiting reload = %+v", r)
	}
	if r := e.get(t, reload); r.State != model.RolloutRunning || r.LeaseOwner != "" {
		t.Fatalf("waiting reload = %+v", r)
	}
	if got := a.commands(); len(got) != 0 {
		t.Fatalf("commands = %v", got)
	}
	// The server reports: the next pass finishes it.
	a.mu.Lock()
	a.varsRev[srv1] = 4
	a.mu.Unlock()
	a.report(srv1)
	e.passUntil(t, func() bool { return e.get(t, reload).State == model.RolloutDone })
	if r := e.get(t, reload); r.Done != 1 {
		t.Fatalf("reload = %+v", r)
	}
}

// passUntil runs passes (a parked rollout is due again after the poll, to
// the second) until cond holds, at most 5 seconds.
func (e *env) passUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("not done within 5s of passes")
		}
		e.uc.pass(dbtest.Ctx(t))
		time.Sleep(20 * time.Millisecond)
	}
}

// waitingScope is a reload scope no server satisfies for the next hour: the
// server never reported the revision.
func waitingScope(rev int64) []byte {
	raw, _ := json.Marshal(model.ReloadScope{Names: []string{"N"}, ServerIDs: []string{srv1}, Revisions: map[string]int64{srv1: rev}, SettleBy: time.Now().Add(time.Hour).UTC()})
	return raw
}

// Waiting rollouts are parked, not freed, so they do not fill the claimable
// list's limit and starve the newer rollouts behind them.
func TestManyWaitingReloadsDoNotStarveANewerRollout(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	for n := range 12 {
		id := dbtest.ID(400 + n)
		if _, ok, err := e.uc.queue(ctx, model.Rollout{ID: id, OrgID: org1, Kind: model.RolloutVarsReload, Scope: waitingScope(9),
			ScopeKey: "waiting-" + id, CreatedBy: "u1"}); err != nil || !ok {
			t.Fatalf("queue %d: %v %v", n, ok, err)
		}
	}
	e.uc.kinds["fake"] = &fakeKind{todo: []string{"x1"}}
	last := e.queueFake(t, 500, "s") // its id sorts after the waiting ones (created in the same second)
	if !e.uc.pass(ctx) {
		t.Fatal("the waiting rollouts were not reported")
	}
	if r := e.get(t, last); r.State != model.RolloutDone {
		t.Fatalf("the 13th rollout = %+v: starved by the waiting ones", r)
	}
}

// Stopping a rollout nobody drives (queued, or parked while it waits) ends
// it at once; one with a live lease only gets stop_requested.
func TestStopEndsAnUnleasedRolloutAtOnce(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	queued := e.activeReload(t, org1)
	v, err := e.uc.Stop(ctx, org1, queued.ID)
	if err != nil || v.State != "stopped" || !v.StopRequested || v.FinishedAt == nil {
		t.Fatalf("stop a queued rollout = %+v, %v", v, err)
	}
	if evs := e.pub.events(); len(evs) == 0 || evs[len(evs)-1].State != "stopped" {
		t.Fatalf("events = %+v", evs)
	}
	// A new reload queues right away (nothing active to refuse it).
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	// A rollout parked by the worker ends at once too.
	scope := smtpOnSrv1()
	scope.Revisions[srv1] = 4 // never reported: it parks
	if _, err := e.uc.Stop(ctx, org1, e.activeReload(t, org1).ID); err != nil {
		t.Fatal(err)
	}
	if err := e.uc.QueueReload(ctx, "u1", org1, scope); err != nil {
		t.Fatal(err)
	}
	parked := e.activeReload(t, org1).ID
	e.uc.pass(ctx)
	if r := e.get(t, parked); r.LeaseOwner != "" || r.LeaseUntil == nil || r.State != model.RolloutRunning {
		t.Fatalf("not parked: %+v", r)
	}
	if v, err := e.uc.Stop(ctx, org1, parked); err != nil || v.State != "stopped" {
		t.Fatalf("stop a parked rollout = %+v, %v", v, err)
	}
	// Another org cannot stop it, and a leased one only gets the request.
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	leased := e.activeReload(t, org1).ID
	if ok, err := e.repo.Claim(ctx, leased, "w9", time.Now(), time.Now().Add(time.Minute)); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if _, err := e.uc.Stop(ctx, org2, leased); !errors.Is(err, model.ErrRolloutNotFound) {
		t.Fatalf("another org's stop: %v", err)
	}
	if v, err := e.uc.Stop(ctx, org1, leased); err != nil || v.State != "running" || !v.StopRequested {
		t.Fatalf("stop a leased rollout = %+v, %v", v, err)
	}
}

func TestRunLooksAgainShortlyWhileARolloutWaits(t *testing.T) {
	e, a := reloadEnv(t, stale("a1", "SMTP_PASS"))
	ctx := dbtest.Ctx(t)
	scope := smtpOnSrv1()
	scope.Revisions[srv1] = 4
	if err := e.uc.QueueReload(ctx, "u1", org1, scope); err != nil {
		t.Fatal(err)
	}
	id := e.activeReload(t, org1).ID
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.uc.Run(runCtx) // idle tick: one hour; only the wait poll can bring the next pass
	eventually(t, "the first pass", func() bool { return e.get(t, id).State == model.RolloutRunning })
	a.mu.Lock()
	a.varsRev[srv1] = 4
	a.mu.Unlock()
	a.report(srv1)
	eventually(t, "the rollout to finish without a wake", func() bool { return e.get(t, id).State == model.RolloutDone })
}

func TestQueueReloadRefusesAStopRequestedRollout(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	before := e.activeReload(t, org1)
	// A worker holds it, so the stop is only a request.
	if ok, err := e.repo.Claim(ctx, before.ID, "w9", time.Now(), time.Now().Add(time.Minute)); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if v, err := e.uc.Stop(ctx, org1, before.ID); err != nil || v.State != "running" || !v.StopRequested {
		t.Fatalf("stop = %+v, %v", v, err)
	}
	err := e.uc.QueueReload(ctx, "u1", org1, model.ReloadScope{Names: []string{"S3_KEY"}, ServerIDs: []string{srv1}})
	if !errors.Is(err, model.ErrRolloutStopping) || !errors.Is(err, model.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	after := e.get(t, before.ID)
	if after.ScopeRev != before.ScopeRev || !bytes.Equal(after.Scope, before.Scope) {
		t.Fatalf("the stopping rollout's scope changed: %+v", after)
	}
	// Once it has stopped, a reload queues a new one.
	if ok, err := e.repo.Finish(ctx, before.ID, "w9", model.RolloutStopped, "", "", ""); err != nil || !ok {
		t.Fatalf("finish: %v %v", ok, err)
	}
	if r := e.get(t, before.ID); r.State != model.RolloutStopped {
		t.Fatalf("rollout = %+v", r)
	}
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	if now := e.activeReload(t, org1); now.ID == before.ID {
		t.Fatal("joined the stopped rollout")
	}
}

// activeOrLast is the org's newest vars_reload rollout.
func (e *env) activeOrLast(t *testing.T, orgID string) model.Rollout {
	t.Helper()
	list, err := e.repo.ListRollouts(dbtest.Ctx(t), orgID, model.RolloutVarsReload, orgID)
	if err != nil || len(list) == 0 {
		t.Fatalf("list = %v, %v", list, err)
	}
	return list[0]
}

func TestReloadSkipsAStoppedAppAndStopsAtAFailure(t *testing.T) {
	e, a := reloadEnv(t, stale("a1", "SMTP_PASS"), stale("a2", "SMTP_PASS"), stale("a3", "SMTP_PASS"))
	ctx := dbtest.Ctx(t)
	a.mu.Lock()
	a.skip["a1"] = true // stopped by the time the agent got to it
	a.fail["a2"] = "reload after a change to SMTP_PASS failed: docker compose up: exit status 1"
	a.mu.Unlock()
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	e.uc.pass(ctx)
	r := e.activeOrLast(t, org1)
	if r.State != model.RolloutFailed || r.Skipped != 1 || !slices.Equal(r.SkipIDs, []string{"a1"}) || r.CurrentAppID != "a2" ||
		r.Error != "reload after a change to SMTP_PASS failed: docker compose up: exit status 1" {
		t.Fatalf("rollout = %+v", r)
	}
	if got := a.commands(); !slices.Equal(got, []string{"vars.reload a1", "vars.reload a2"}) {
		t.Fatalf("commands = %v", got)
	}
}

// An app deleted while its reload was queued or in flight is skipped, not
// a failure; a failure of an app the catalog still has stays one.
func TestReloadSkipsAnAppDeletedMeanwhile(t *testing.T) {
	e, a := reloadEnv(t, stale("a1", "SMTP_PASS"), stale("a2", "SMTP_PASS"), stale("a3", "SMTP_PASS"))
	ctx := dbtest.Ctx(t)
	dbtest.Exec(t, e.d, `DELETE FROM apps WHERE app_id = ?`, "a1")
	a.mu.Lock()
	a.fail["a1"] = "the app was deleted"
	a.fail["a3"] = "reload failed"
	a.mu.Unlock()
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	e.uc.pass(ctx)
	r := e.activeOrLast(t, org1)
	if r.State != model.RolloutFailed || r.Skipped != 1 || r.Done != 1 || !slices.Equal(r.SkipIDs, []string{"a1"}) ||
		r.CurrentAppID != "a3" || r.Error != "reload failed" {
		t.Fatalf("rollout = %+v", r)
	}
}

func TestReloadThatLeavesTheAppStaleFails(t *testing.T) {
	e, a := reloadEnv(t, stale("a1", "SMTP_PASS"))
	a.mu.Lock()
	a.stuck["a1"] = true
	a.mu.Unlock()
	ctx := dbtest.Ctx(t)
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	e.uc.pass(ctx)
	if r := e.activeOrLast(t, org1); r.State != model.RolloutFailed || r.Error != "the app is still stale after the reload: SMTP_PASS" {
		t.Fatalf("rollout = %+v", r)
	}
	if got := a.commands(); len(got) != 1 {
		t.Fatalf("commands = %v: the app must not be reloaded in a loop", got)
	}
}

func TestAnExtensionLandingAtTheEndIsNotLost(t *testing.T) {
	e, a := reloadEnv(t, stale("a1", "SMTP_PASS"), stale("b1", "S3_KEY"))
	ctx := dbtest.Ctx(t)
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	e.uc.d.Repo = &completeHook{RolloutRepository: e.repo, before: func() {
		// S3_KEY was saved with reload while the worker found nothing left.
		if err := e.uc.QueueReload(ctx, "u1", org1, model.ReloadScope{Names: []string{"S3_KEY"}, ServerIDs: []string{srv1}}); err != nil {
			t.Error(err)
		}
	}}
	e.uc.pass(ctx)
	if r := e.activeOrLast(t, org1); r.State != model.RolloutDone || r.Done != 2 || r.ScopeRev != 2 {
		t.Fatalf("rollout = %+v", r)
	}
	if got := a.commands(); !slices.Equal(got, []string{"vars.reload a1", "vars.reload b1"}) {
		t.Fatalf("commands = %v", got)
	}
}

func TestAnUnreadableScopeFailsTheRollout(t *testing.T) {
	e := newEnv(t)
	id := dbtest.ID(301)
	if _, ok, err := e.uc.queue(dbtest.Ctx(t), model.Rollout{ID: id, OrgID: org1, Kind: model.RolloutVarsReload, Scope: []byte("{"), ScopeKey: org1}); err != nil || !ok {
		t.Fatalf("queue: %v %v", ok, err)
	}
	e.uc.pass(dbtest.Ctx(t))
	if r := e.get(t, id); r.State != model.RolloutFailed || r.Error != errBadScope.Error() {
		t.Fatalf("rollout = %+v", r)
	}
}

// completeHook runs before() once, right before the first Complete.
type completeHook struct {
	port.RolloutRepository
	before func()
	ran    bool
}

func (c *completeHook) Complete(ctx context.Context, id, owner string, rev int64) (bool, error) {
	if !c.ran {
		c.ran = true
		c.before()
	}
	return c.RolloutRepository.Complete(ctx, id, owner, rev)
}
