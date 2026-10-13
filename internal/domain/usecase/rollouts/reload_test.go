package rollouts

import (
	"context"
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
// revision 3, reported once.
func reloadEnv(t *testing.T, apps ...command.AppStatus) (*env, *fakeAgent) {
	t.Helper()
	e := newEnv(t)
	a, _ := e.withAgent(t)
	a.mu.Lock()
	a.varsRev[srv1] = 3
	a.mu.Unlock()
	for _, st := range apps {
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
	passed := make(chan struct{})
	go func() { e.uc.pass(context.Background()); close(passed) }()
	time.Sleep(50 * time.Millisecond)
	if got := a.commands(); len(got) != 0 {
		t.Fatalf("reloaded before the server reported the change: %v", got)
	}
	a.mu.Lock()
	a.varsRev[srv1] = 4
	a.mu.Unlock()
	a.report(srv1)
	<-passed
	if r := e.get(t, id); r.State != model.RolloutDone || r.Done != 1 {
		t.Fatalf("rollout = %+v", r)
	}

	// A server that never reports the change is left out after SettleBy.
	if err := e.uc.QueueReload(ctx, "u1", org1, model.ReloadScope{Names: []string{"X"}, ServerIDs: []string{srv2}, Revisions: map[string]int64{srv2: 9}}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	e.uc.pass(ctx)
	if r := e.activeOrLast(t, org1); r.State != model.RolloutDone || r.Done != 0 || time.Since(start) < e.uc.settle/2 {
		t.Fatalf("unsettled rollout = %+v after %s", r, time.Since(start))
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
