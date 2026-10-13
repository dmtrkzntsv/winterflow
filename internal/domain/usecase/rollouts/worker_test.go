package rollouts

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/infra/db/dbtest"
)

// fakeKind steps through todo in order: run answers results[app] (done
// by default, which takes the app off todo) and, for an app in block,
// first waits for the channel or the step's context.
type fakeKind struct {
	mu      sync.Mutex
	todo    []string
	results map[string]stepResult
	block   map[string]chan struct{}
	ran     []string
	gaveUp  chan error
}

func (f *fakeKind) next(_ context.Context, r model.Rollout) (pick, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var left []string
	for _, id := range f.todo {
		if !slices.Contains(r.SkipIDs, id) {
			left = append(left, id)
		}
	}
	if len(left) == 0 {
		return pick{}, nil
	}
	return pick{appID: left[0], serverID: srv1, skipID: left[0], remaining: int64(len(left))}, nil
}

func (f *fakeKind) run(ctx context.Context, _ model.Rollout, p pick) stepResult {
	f.mu.Lock()
	ch := f.block[p.appID]
	f.mu.Unlock()
	if ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
			f.gaveUp <- ctx.Err()
			return failed("cancelled")
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, p.appID)
	res, ok := f.results[p.appID]
	if !ok {
		res = done()
	}
	if res.outcome == stepDone {
		f.todo = slices.DeleteFunc(f.todo, func(id string) bool { return id == p.appID })
	}
	return res
}

func (f *fakeKind) runs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.ran)
}

// queueFake queues a rollout of the fake kind for scope.
func (e *env) queueFake(t *testing.T, n int, scope string) string {
	t.Helper()
	id := dbtest.ID(100 + n)
	if _, ok, err := e.uc.queue(dbtest.Ctx(t), model.Rollout{ID: id, OrgID: org1, Kind: "fake", Scope: []byte(`{}`), ScopeKey: scope, CreatedBy: "u1"}); err != nil || !ok {
		t.Fatalf("queue: %v %v", ok, err)
	}
	return id
}

func TestPassStepsThroughToDone(t *testing.T) {
	e := newEnv(t)
	k := &fakeKind{todo: []string{"a1", "a2", "a3"}, results: map[string]stepResult{"a2": skipped()}}
	e.uc.kinds["fake"] = k
	id := e.queueFake(t, 1, "s")
	e.uc.pass(dbtest.Ctx(t))

	r := e.get(t, id)
	if r.State != model.RolloutDone || r.Done != 2 || r.Skipped != 1 || r.Total != 3 || !slices.Equal(r.SkipIDs, []string{"a2"}) ||
		r.CurrentAppID != "" || r.LeaseOwner != "" || r.FinishedAt == nil {
		t.Fatalf("rollout = %+v", r)
	}
	if got := k.runs(); !slices.Equal(got, []string{"a1", "a2", "a3"}) {
		t.Fatalf("runs = %v", got)
	}
	evs := e.pub.events()
	if len(evs) < 3 || evs[0].State != "queued" || evs[len(evs)-1].State != "done" || evs[len(evs)-1].Done != 2 {
		t.Fatalf("events = %+v", evs)
	}
	// Each event reached every member; a step in flight names its app.
	if e.pub.n != len(evs)*len(members) || !slices.ContainsFunc(evs, func(v model.RolloutView) bool {
		return v.State == "running" && v.CurrentAppID == "a3" && v.Total == 3 && v.Done == 1 && v.Skipped == 1
	}) {
		t.Fatalf("events = %+v (%d sent)", evs, e.pub.n)
	}
}

func TestFirstFailureEndsTheRollout(t *testing.T) {
	e := newEnv(t)
	k := &fakeKind{todo: []string{"a1", "a2", "a3"}, results: map[string]stepResult{"a2": failed("compose up failed")}}
	e.uc.kinds["fake"] = k
	id := e.queueFake(t, 1, "s")
	e.uc.pass(dbtest.Ctx(t))

	r := e.get(t, id)
	if r.State != model.RolloutFailed || r.Error != "compose up failed" || r.CurrentAppID != "a2" || r.CurrentServerID != srv1 || r.Done != 1 {
		t.Fatalf("rollout = %+v", r)
	}
	if got := k.runs(); !slices.Equal(got, []string{"a1", "a2"}) {
		t.Fatalf("runs = %v: nothing may follow a failure", got)
	}
}

func TestStopEndsAfterTheStepInFlight(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	k := &fakeKind{todo: []string{"a1", "a2"}, block: map[string]chan struct{}{"a1": release}}
	e.uc.kinds["fake"] = k
	id := e.queueFake(t, 1, "s")
	passed := make(chan struct{})
	go func() { e.uc.pass(context.Background()); close(passed) }()
	eventually(t, "a1 in flight", func() bool { return e.get(t, id).CurrentAppID == "a1" })

	ctx := dbtest.Ctx(t)
	if _, err := e.uc.Stop(ctx, org2, id); !errors.Is(err, model.ErrRolloutNotFound) {
		t.Fatalf("another org's stop: %v", err)
	}
	v, err := e.uc.Stop(ctx, org1, id)
	if err != nil || !v.StopRequested || v.State != "running" {
		t.Fatalf("stop = %+v, %v", v, err)
	}
	close(release)
	<-passed
	r := e.get(t, id)
	if r.State != model.RolloutStopped || r.Done != 1 || r.CurrentAppID != "" {
		t.Fatalf("rollout = %+v", r)
	}
	if got := k.runs(); !slices.Equal(got, []string{"a1"}) {
		t.Fatalf("runs = %v", got)
	}
	// Stopping an ended rollout answers it as it is.
	if v, err := e.uc.Stop(ctx, org1, id); err != nil || v.State != "stopped" {
		t.Fatalf("stop again = %+v, %v", v, err)
	}
}

func TestAnotherProcessTakesOverAnExpiredLease(t *testing.T) {
	e := newEnv(t)
	t0 := time.Now().UTC().Truncate(time.Second)
	// Process 1 (its clock stuck at t0) holds a1 forever.
	k1 := &fakeKind{todo: []string{"a1", "a2"}, block: map[string]chan struct{}{"a1": make(chan struct{})}, gaveUp: make(chan error, 1)}
	e.uc.kinds["fake"] = k1
	e.uc.now = func() time.Time { return t0 }
	id := e.queueFake(t, 1, "s")
	go e.uc.pass(context.Background())
	eventually(t, "a1 in flight", func() bool { return e.get(t, id).CurrentAppID == "a1" })

	// Process 2 runs past process 1's lease: it claims the rollout and
	// redoes a1, and process 1's next renewal finds the lease gone and
	// abandons its step without counting it.
	uc2 := e.newUseCase()
	k2 := &fakeKind{todo: []string{"a1", "a2"}}
	uc2.kinds["fake"] = k2
	uc2.now = func() time.Time { return t0.Add(5 * time.Minute) }
	uc2.pass(dbtest.Ctx(t))
	select {
	case err := <-k1.gaveUp:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("process 1's step ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process 1 kept its step after losing the lease")
	}
	r := e.get(t, id)
	if r.State != model.RolloutDone || r.Done != 2 || len(k1.runs()) != 0 || !slices.Equal(k2.runs(), []string{"a1", "a2"}) {
		t.Fatalf("rollout = %+v; runs 1 = %v, 2 = %v", r, k1.runs(), k2.runs())
	}
}

func TestShutdownGivesTheLeaseBack(t *testing.T) {
	e := newEnv(t)
	k := &fakeKind{todo: []string{"a1", "a2"}, block: map[string]chan struct{}{"a1": make(chan struct{})}, gaveUp: make(chan error, 1)}
	e.uc.kinds["fake"] = k
	id := e.queueFake(t, 1, "s")
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { e.uc.Run(ctx); close(stopped) }()
	eventually(t, "a1 in flight", func() bool { return e.get(t, id).CurrentAppID == "a1" })
	cancel()
	<-stopped
	// A restarted process need not wait for the lease to expire.
	r := e.get(t, id)
	if r.State != model.RolloutRunning || r.LeaseOwner != "" || r.LeaseUntil != nil {
		t.Fatalf("rollout after shutdown = %+v", r)
	}
	if ids, err := e.repo.ListClaimable(dbtest.Ctx(t), time.Now()); err != nil || !slices.Contains(ids, id) {
		t.Fatalf("claimable = %v, %v", ids, err)
	}
}

func TestUnknownKindFails(t *testing.T) {
	e := newEnv(t)
	id := e.queueFake(t, 1, "s") // no runner for "fake"
	e.uc.pass(dbtest.Ctx(t))
	if r := e.get(t, id); r.State != model.RolloutFailed || r.Error != "unknown rollout kind fake" {
		t.Fatalf("rollout = %+v", r)
	}
}

func TestEndedRolloutsArePrunedPerScope(t *testing.T) {
	e := newEnv(t)
	e.uc.kinds["fake"] = &fakeKind{}
	for n := 1; n <= keepEnded+2; n++ {
		e.queueFake(t, n, "s")
		e.uc.pass(dbtest.Ctx(t))
	}
	e.queueFake(t, 50, "other")
	e.uc.pass(dbtest.Ctx(t))
	list, err := e.repo.ListRollouts(dbtest.Ctx(t), org1, "fake", "s")
	if err != nil || len(list) != keepEnded {
		t.Fatalf("scope s keeps %d, %v", len(list), err)
	}
	if other, _ := e.repo.ListRollouts(dbtest.Ctx(t), org1, "fake", "other"); len(other) != 1 {
		t.Fatalf("scope other = %+v", other)
	}
}

// countingRepo counts the worker's idle query.
type countingRepo struct {
	port.RolloutRepository
	n atomic.Int64
}

func (c *countingRepo) ListClaimable(ctx context.Context, now time.Time) ([]string, error) {
	c.n.Add(1)
	return c.RolloutRepository.ListClaimable(ctx, now)
}

func TestIdleWorkerQueriesOnlyOnTickOrWake(t *testing.T) {
	e := newEnv(t)
	counter := &countingRepo{RolloutRepository: e.repo}
	e.uc.d.Repo = counter
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.uc.Run(ctx)
	eventually(t, "the first pass", func() bool { return counter.n.Load() == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := counter.n.Load(); n != 1 {
		t.Fatalf("an idle worker queried %d times before its tick", n)
	}
	e.uc.Wake()
	eventually(t, "the woken pass", func() bool { return counter.n.Load() == 2 })
}

func TestListValidatesKindAndScope(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	if _, err := e.uc.List(ctx, org1, "bogus", ""); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, err := e.uc.List(ctx, org1, model.RolloutVarsReload, org2); !errors.Is(err, model.ErrRolloutNotFound) {
		t.Fatalf("another org as scope: %v", err)
	}
	if _, err := e.uc.List(ctx, org1, model.RolloutTemplateUpgrade, dbtest.ID(77)); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("unknown template: %v", err)
	}
	if _, ok, err := e.uc.queue(ctx, model.Rollout{ID: dbtest.ID(201), OrgID: org1, Kind: model.RolloutVarsReload, Scope: []byte(`{}`), ScopeKey: org1}); err != nil || !ok {
		t.Fatalf("queue: %v %v", ok, err)
	}
	if list, err := e.uc.List(ctx, org1, model.RolloutVarsReload, ""); err != nil || len(list) != 1 || list[0].ScopeKey != org1 || list[0].State != "queued" {
		t.Fatalf("org1 list = %+v, %v", list, err)
	}
	if list, err := e.uc.List(ctx, org2, model.RolloutVarsReload, ""); err != nil || len(list) != 0 {
		t.Fatalf("org2 list = %+v, %v", list, err)
	}
}

func TestAwaitRunning(t *testing.T) {
	e := newEnv(t)
	since := time.Now()
	ctx := dbtest.Ctx(t)
	app := func(code command.ContainerStatusCode) []command.AppStatus {
		return []command.AppStatus{{AppID: "a1", StatusCode: code}}
	}
	// A report from before the step does not count; two later Active ones,
	// healthSettle apart, do.
	e.reports.set(srv1, status.Report{Apps: app(command.ContainerStatusActive), Received: since.Add(-time.Second)})
	stop, fed := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(fed)
		time.Sleep(30 * time.Millisecond)
		if e.reports.setRead(srv1, status.Report{Apps: app(command.ContainerStatusActive), Received: since.Add(time.Second)}, stop) {
			e.reports.set(srv1, status.Report{Apps: app(command.ContainerStatusActive), Received: since.Add(time.Second + e.uc.healthSettle)})
		}
	}()
	_, err := e.uc.awaitRunning(ctx, srv1, "a1", since)
	close(stop)
	<-fed
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	ctr := func(name string, code command.ContainerStatusCode, exit int32) command.ContainerStatus {
		return command.ContainerStatus{Name: name, StatusCode: code, ExitCode: exit}
	}
	withContainers := func(agg command.ContainerStatusCode, cs ...command.ContainerStatus) []command.AppStatus {
		return []command.AppStatus{{AppID: "a1", StatusCode: agg, Containers: cs}}
	}
	healthy := withContainers(command.ContainerStatusActive, ctr("web", command.ContainerStatusActive, 0))
	oneShot := withContainers(command.ContainerStatusIdle, ctr("web", command.ContainerStatusActive, 0), ctr("migrate", command.ContainerStatusStopped, 0))
	restarting := withContainers(command.ContainerStatusIdle, ctr("web", command.ContainerStatusRestarting, 0))
	cases := []struct {
		name    string
		reports [][]command.AppStatus // received 10ms apart (healthSettle is 30ms)
		want    string                // "" passes
		fast    bool                  // answers well before the bound
	}{
		// The agent aggregates a restarting container to Idle; a crash seen
		// fails at once.
		{"crash loop", [][]command.AppStatus{withContainers(command.ContainerStatusIdle, ctr("web", command.ContainerStatusActive, 0), ctr("worker", command.ContainerStatusRestarting, 0))}, "container worker is restarting", true},
		{"restarting only", [][]command.AppStatus{restarting}, "container web is restarting", true},
		{"problematic", [][]command.AppStatus{withContainers(command.ContainerStatusProblematic, ctr("web", command.ContainerStatusActive, 0), ctr("job", command.ContainerStatusProblematic, 3))}, "container job exited with code 3", true},
		// One healthy report is not enough, two too close together neither.
		{"one-shot, one report", [][]command.AppStatus{oneShot}, "healthy on one status report only", false},
		{"healthy twice, too close", [][]command.AppStatus{healthy, healthy}, "healthy on one status report only", false},
		{"one-shot finished", [][]command.AppStatus{oneShot, oneShot, oneShot, oneShot}, "", true},
		{"all active", [][]command.AppStatus{healthy, healthy, healthy, healthy}, "", true},
		// A crash between two healthy reports fails the step.
		{"restarting between healthy", [][]command.AppStatus{healthy, restarting, healthy, healthy}, "container web is restarting", true},
		// Another unhealthy report in between starts the count again.
		{"stopped between healthy", [][]command.AppStatus{healthy, healthy, withContainers(command.ContainerStatusStopped, ctr("web", command.ContainerStatusStopped, 0)), healthy}, "healthy on one status report only", false},
		{"active beside paused", [][]command.AppStatus{withContainers(command.ContainerStatusIdle, ctr("web", command.ContainerStatusActive, 0), ctr("side", command.ContainerStatusIdle, 0))}, "", false},
		{"stopped", [][]command.AppStatus{withContainers(command.ContainerStatusStopped, ctr("web", command.ContainerStatusStopped, 0))}, "not running 200ms later (stopped)", false},
		{"idle without containers", [][]command.AppStatus{app(command.ContainerStatusIdle)}, "the app has no containers", false},
		{"not listed", [][]command.AppStatus{nil}, "the app has no containers", false},
	}
	for _, tc := range cases {
		e.reports.set(srv1, status.Report{Apps: tc.reports[0], Received: since.Add(time.Second)})
		stop, fed := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(fed)
			for i, apps := range tc.reports[1:] {
				if !e.reports.setRead(srv1, status.Report{Apps: apps, Received: since.Add(time.Second + time.Duration(i+1)*10*time.Millisecond)}, stop) {
					return
				}
			}
		}()
		start := time.Now()
		_, err := e.uc.awaitRunning(ctx, srv1, "a1", since)
		close(stop)
		<-fed
		if (tc.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
		if took := time.Since(start); tc.fast && took > 100*time.Millisecond {
			t.Errorf("%s: took %v, want an answer well before the bound", tc.name, took)
		}
	}
	if _, err := e.uc.awaitRunning(ctx, srv2, "a1", since); err == nil || !strings.Contains(err.Error(), "no status report") {
		t.Fatalf("silent server: %v", err)
	}
}

func TestAwaitResultBounds(t *testing.T) {
	e := newEnv(t)
	e.uc.cmdWait = 20 * time.Millisecond
	ch := make(chan port.CommandResult, 1)
	if _, err := e.uc.awaitResult(dbtest.Ctx(t), ch); err == nil || err.Error() != "no answer from the server within 20ms" {
		t.Fatalf("silent agent: %v", err)
	}
	ch <- port.CommandResult{}
	if _, err := e.uc.awaitResult(dbtest.Ctx(t), ch); err == nil || err.Error() != "the agent reported a failure" {
		t.Fatalf("failure without a message: %v", err)
	}
}
