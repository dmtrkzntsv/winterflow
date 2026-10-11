package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/pkg/logger"
)

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// fakeClock is a manual clock: After fires once Advance passes its time;
// armed reports every wait the scheduler starts.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeTimer
	armed  chan time.Duration
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

func newClock() *fakeClock { return &fakeClock{now: t0, armed: make(chan time.Duration, 100)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.timers = append(c.timers, fakeTimer{at: c.now.Add(d), ch: ch})
	c.armed <- d
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.timers[:0]
	for _, tm := range c.timers {
		if !tm.at.After(c.now) {
			tm.ch <- c.now
			continue
		}
		kept = append(kept, tm)
	}
	c.timers = kept
}

// fakeEngine plans plan and records runs; a run answers result, or waits on
// block (or its context) first.
type fakeEngine struct {
	mu     sync.Mutex
	plan   []dockercompose.PlannedBackup
	runs   chan dockercompose.BackupJob
	block  chan struct{}
	ignore bool // keep blocking past the context
	// blockOnly limits block to one app's runs; exitDelay is how long a run
	// takes to return once its context is done; exits counts returned runs.
	blockOnly string
	exitDelay time.Duration
	exits     atomic.Int32
	result    func(dockercompose.BackupJob) (dockercompose.BackupResult, error)
}

func newEngine(plan ...dockercompose.PlannedBackup) *fakeEngine {
	return &fakeEngine{plan: plan, runs: make(chan dockercompose.BackupJob, 100)}
}

func (e *fakeEngine) BackupPlan() ([]dockercompose.PlannedBackup, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]dockercompose.PlannedBackup(nil), e.plan...), nil
}

func (e *fakeEngine) setPlan(plan ...dockercompose.PlannedBackup) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.plan = plan
}

func (e *fakeEngine) RunBackup(ctx context.Context, job dockercompose.BackupJob) (dockercompose.BackupResult, error) {
	defer e.exits.Add(1)
	e.runs <- job
	e.mu.Lock()
	block, ignore, result, only, delay := e.block, e.ignore, e.result, e.blockOnly, e.exitDelay
	e.mu.Unlock()
	if block != nil && (only == "" || only == job.AppID) {
		if ignore {
			<-block
		} else {
			select {
			case <-block:
			case <-ctx.Done():
				time.Sleep(delay)
				return dockercompose.BackupResult{}, ctx.Err()
			}
		}
	}
	if result != nil {
		return result(job)
	}
	return dockercompose.BackupResult{Point: &command.RestorePoint{ID: "p", Size: 42}, Fingerprint: "fp-" + job.Backup, Size: 42}, nil
}

func planned(app, name string, typ recipe.Type, enabled bool, every string) dockercompose.PlannedBackup {
	return dockercompose.PlannedBackup{AppID: app, EffectiveBackup: recipe.EffectiveBackup{
		Name: name, Type: typ, Enabled: enabled, Storage: "disk", Every: every, Service: "svc"}}
}

// start runs a scheduler on a fake clock (opts adjust it first) until the
// test ends.
func start(t *testing.T, e *fakeEngine, opts ...func(*Scheduler)) (*Scheduler, *fakeClock, string) {
	t.Helper()
	dir := t.TempDir()
	s := New(e, dir, logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
	clock := newClock()
	s.clock = clock
	for _, o := range opts {
		o(s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s, clock, dir
}

// idle waits until the scheduler waits on its timer and returns the wait.
func idle(t *testing.T, c *fakeClock) time.Duration {
	t.Helper()
	select {
	case d := <-c.armed:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("the scheduler never went idle")
		return 0
	}
}

func ran(t *testing.T, e *fakeEngine) dockercompose.BackupJob {
	t.Helper()
	select {
	case j := <-e.runs:
		return j
	case <-time.After(5 * time.Second):
		t.Fatal("no run")
		return dockercompose.BackupJob{}
	}
}

func noRun(t *testing.T, e *fakeEngine) {
	t.Helper()
	select {
	case j := <-e.runs:
		t.Fatalf("unexpected run %+v", j)
	default:
	}
}

// Backups enabled together start at stable offsets within their interval,
// earliest first, then every interval after their last attempt; manual and
// custom backups and backups that are off are never scheduled.
func TestScheduleOffsetsAndOrder(t *testing.T) {
	a, b := planned("app-1", "db", recipe.TypeSQLite, true, "1h"), planned("app-2", "db", recipe.TypeSQLite, true, "1h")
	e := newEngine(a, b,
		planned("app-1", "files", recipe.TypeFiles, true, recipe.Manual),
		planned("app-1", "replica", recipe.TypeCustom, true, ""),
		planned("app-3", "db", recipe.TypeSQLite, false, "1h"))
	_, clock, _ := start(t, e)
	offA, offB := Offset("app-1", "db", time.Hour), Offset("app-2", "db", time.Hour)
	if offA == offB || offA >= time.Hour || offB >= time.Hour || Offset("app-1", "db", time.Hour) != offA {
		t.Fatalf("offsets %s %s", offA, offB)
	}
	first, second := "app-1", "app-2"
	if offB < offA {
		first, second, offA, offB = "app-2", "app-1", offB, offA
	}
	if wait := idle(t, clock); wait != offA {
		t.Fatalf("first wait = %s, want %s", wait, offA)
	}
	clock.Advance(offB)
	if j := ran(t, e); j.AppID != first || j.Kind != command.PointScheduled {
		t.Fatalf("first run = %+v", j)
	}
	if j := ran(t, e); j.AppID != second {
		t.Fatalf("second run = %+v", j)
	}
	// Next: the first one, an hour after its attempt (at offB).
	if wait := idle(t, clock); wait != time.Hour {
		t.Fatalf("next wait = %s", wait)
	}
	noRun(t, e)
}

func TestStateIsRecordedAndReloaded(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, "5m"))
	s, clock, dir := start(t, e)
	idle(t, clock)
	clock.Advance(5 * time.Minute)
	ran(t, e)
	idle(t, clock)
	raw, err := os.ReadFile(filepath.Join(dir, "backups", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]State
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	got := st["app-1/db"]
	if !got.EnabledAt.Equal(t0) || got.LastAttempt.IsZero() || !got.LastSuccess.Equal(got.LastAttempt) || got.LastSize != 42 || got.Fingerprint != "fp-db" || got.LastError != "" {
		t.Fatalf("state = %+v", got)
	}
	if info, _ := os.Stat(filepath.Join(dir, "backups", "state.json")); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
	h := s.Health("app-1", clock.Now(), nil)
	if len(h) != 1 || h[0].State != command.BackupOK || h[0].LastSuccess != got.LastSuccess.Unix() {
		t.Fatalf("health = %+v", h)
	}
	// A new scheduler reads the state back.
	again := New(e, dir, s.log)
	if st := again.state["app-1/db"]; st == nil || !st.LastAttempt.Equal(got.LastAttempt) {
		t.Fatalf("reloaded = %+v", st)
	}
	// An unreadable state file is an empty state.
	if err := os.WriteFile(filepath.Join(dir, "backups", "state.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fresh := New(e, dir, s.log); len(fresh.state) != 0 {
		t.Fatalf("state = %+v", fresh.state)
	}
}

// Back up now goes ahead of the schedule; a request for a backup already
// running or queued fails at once instead of waiting behind it.
func TestRunNowJoinsAndGoesFirst(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, "5m"), planned("app-1", "media", recipe.TypeFiles, true, recipe.Manual))
	e.block = make(chan struct{})
	s, clock, _ := start(t, e)
	idle(t, clock)
	clock.Advance(10 * time.Minute)
	if j := ran(t, e); j.Backup != "db" {
		t.Fatalf("run = %+v", j)
	}
	// db is running: a request for it fails at once; media queues once and
	// a second request for it fails at once.
	dupRunning := <-s.RunNow("app-1", "db")
	m1 := s.RunNow("app-1", "media")
	dupQueued := <-s.RunNow("app-1", "media")
	for _, o := range []Outcome{dupRunning, dupQueued} {
		if o.Err == nil || !strings.Contains(o.Err.Error(), "is already queued or running") {
			t.Fatalf("duplicate = %+v", o)
		}
	}
	if !strings.Contains(dupRunning.Err.Error(), "a backup of db ") {
		t.Fatalf("error = %v", dupRunning.Err)
	}
	if h := s.Health("app-1", clock.Now(), nil); h[0].State != command.BackupRunning || h[1].State != command.BackupNever {
		t.Fatalf("health = %+v", h)
	}
	close(e.block)
	if j := ran(t, e); j.Backup != "media" || j.Kind != command.PointManual {
		t.Fatalf("manual run = %+v", j)
	}
	if o1 := <-m1; o1.Err != nil || o1.Result.AppID != "app-1" {
		t.Fatalf("outcome = %+v", o1)
	}
	idle(t, clock)
	noRun(t, e)
}

// A run past its deadline fails (the engine stops its helper on the
// context); one that doesn't stop is abandoned after the grace.
func TestRunDeadline(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, recipe.Manual))
	e.block = make(chan struct{})
	defer close(e.block)
	s, clock, _ := start(t, e, func(s *Scheduler) { s.runTimeout, s.grace = 20*time.Millisecond, 20*time.Millisecond })
	idle(t, clock)
	o := <-s.RunNow("app-1", "db")
	if o.Err == nil || !strings.Contains(o.Err.Error(), "deadline exceeded") {
		t.Fatalf("outcome = %+v", o)
	}
	if h := s.Health("app-1", clock.Now(), nil); h[0].State != command.BackupFailing || !strings.Contains(h[0].LastError, "deadline") {
		t.Fatalf("health = %+v", h)
	}
	e.mu.Lock()
	e.ignore = true
	e.mu.Unlock()
	o = <-s.RunNow("app-1", "db")
	if o.Err == nil || !strings.Contains(o.Err.Error(), "did not stop after its time limit") {
		t.Fatalf("abandoned = %+v", o)
	}
	ran(t, e)
	ran(t, e)
}

func TestReloadAndForget(t *testing.T) {
	e := newEngine()
	s, clock, dir := start(t, e)
	if wait := idle(t, clock); wait != idleWait {
		t.Fatalf("idle wait = %s", wait)
	}
	e.setPlan(planned("app-1", "db", recipe.TypeSQLite, true, "5m"), planned("app-2", "db", recipe.TypeSQLite, true, "5m"))
	s.Reload()
	idle(t, clock)
	clock.Advance(5 * time.Minute)
	ran(t, e)
	ran(t, e)
	idle(t, clock)
	// Turned off: no longer scheduled, and its enabled time is cleared.
	e.setPlan(planned("app-1", "db", recipe.TypeSQLite, false, "5m"), planned("app-2", "db", recipe.TypeSQLite, true, "5m"))
	s.Reload()
	idle(t, clock)
	s.mu.Lock()
	if !s.state["app-1/db"].EnabledAt.IsZero() {
		t.Fatal("enabled_at kept for a backup turned off")
	}
	s.mu.Unlock()
	if h := s.Health("app-1", clock.Now(), nil); h[0].State != command.BackupOff {
		t.Fatalf("health = %+v", h)
	}
	// A deleted app: queued requests fail, state pruned, plan dropped.
	e.mu.Lock()
	e.block = make(chan struct{})
	e.mu.Unlock()
	queued := s.RunNow("app-2", "db")
	running := ran(t, e)
	if running.AppID != "app-2" {
		t.Fatalf("run = %+v", running)
	}
	again := s.RunNow("app-2", "other")
	e.setPlan(planned("app-1", "db", recipe.TypeSQLite, false, "5m"))
	s.Forget("app-2")
	if o := <-again; o.Err == nil || o.Err.Error() != "the app was deleted" {
		t.Fatalf("queued = %+v", o)
	}
	if o := <-queued; o.Err == nil {
		t.Fatalf("the deleted app's run was not stopped: %+v", o)
	}
	close(e.block)
	idle(t, clock)
	raw, _ := os.ReadFile(filepath.Join(dir, "backups", "state.json"))
	if strings.Contains(string(raw), "app-2") {
		t.Fatalf("state kept the deleted app: %s", raw)
	}
	if h := s.Health("app-2", clock.Now(), nil); len(h) != 0 {
		t.Fatalf("health = %+v", h)
	}
}

// A run that finds nothing changed is a success.
func TestUnchangedIsASuccess(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, recipe.Manual))
	e.result = func(dockercompose.BackupJob) (dockercompose.BackupResult, error) {
		return dockercompose.BackupResult{Unchanged: true, Fingerprint: "fp"}, nil
	}
	s, clock, _ := start(t, e)
	idle(t, clock)
	o := <-s.RunNow("app-1", "db")
	if o.Err != nil || !o.Result.Unchanged || o.Result.Point != nil {
		t.Fatalf("outcome = %+v", o)
	}
	if h := s.Health("app-1", clock.Now(), nil); h[0].State != command.BackupOK {
		t.Fatalf("health = %+v", h)
	}
	e.mu.Lock()
	e.result = func(dockercompose.BackupJob) (dockercompose.BackupResult, error) {
		return dockercompose.BackupResult{}, errors.New("boom")
	}
	e.mu.Unlock()
	if o := <-s.RunNow("app-1", "db"); o.Err == nil {
		t.Fatal("failure lost")
	}
	if h := s.Health("app-1", clock.Now(), nil); h[0].State != command.BackupFailing {
		t.Fatalf("health = %+v", h)
	}
	ran(t, e)
	ran(t, e)
}

// countingEngine counts how often the plan is read.
type countingEngine struct {
	*fakeEngine
	mu    sync.Mutex
	plans int
}

func (c *countingEngine) BackupPlan() ([]dockercompose.PlannedBackup, error) {
	c.mu.Lock()
	c.plans++
	c.mu.Unlock()
	return c.fakeEngine.BackupPlan()
}

// A run refused before it started (the backup was turned off, or the app
// changed, without a Reload) records no failure; the plan is reread, and a
// scheduled run counts as an attempt so it isn't picked again at once.
func TestNotRunnableRereadsThePlan(t *testing.T) {
	e := &countingEngine{fakeEngine: newEngine(planned("app-1", "db", recipe.TypeSQLite, true, "5m"))}
	e.result = func(dockercompose.BackupJob) (dockercompose.BackupResult, error) {
		return dockercompose.BackupResult{}, fmt.Errorf("refused: %w", dockercompose.ErrNotRunnable)
	}
	s := New(e, t.TempDir(), logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
	clock := newClock()
	s.clock = clock
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	idle(t, clock)
	clock.Advance(5 * time.Minute)
	ran(t, e.fakeEngine)
	if wait := idle(t, clock); wait != 5*time.Minute {
		t.Fatalf("next wait = %s: the refused run was not counted as an attempt", wait)
	}
	e.mu.Lock()
	plans := e.plans
	e.mu.Unlock()
	if plans != 2 {
		t.Fatalf("plan read %d times, want 2", plans)
	}
	s.mu.Lock()
	st := *s.state["app-1/db"]
	s.mu.Unlock()
	if st.LastError != "" || !st.LastSuccess.IsZero() || st.LastAttempt.IsZero() {
		t.Fatalf("state = %+v", st)
	}
	// Back up now refused: no attempt either.
	if o := <-s.RunNow("app-1", "db"); !errors.Is(o.Err, dockercompose.ErrNotRunnable) {
		t.Fatalf("outcome = %+v", o)
	}
	ran(t, e.fakeEngine)
}

func TestHealthStates(t *testing.T) {
	now := t0
	sched := recipe.EffectiveBackup{Name: "db", Type: recipe.TypeSQLite, Enabled: true, Every: "1h"}
	manual := recipe.EffectiveBackup{Name: "db", Type: recipe.TypeSQLite, Enabled: true, Every: recipe.Manual}
	custom := recipe.EffectiveBackup{Name: "r", Type: recipe.TypeCustom, Enabled: true, Service: "litestream"}
	off := recipe.EffectiveBackup{Name: "db", Type: recipe.TypeSQLite, Every: "1h"}
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	up := func(s string) bool { return s == "litestream" }
	for _, tc := range []struct {
		name    string
		b       recipe.EffectiveBackup
		st      *State
		running bool
		svc     func(string) bool
		want    command.BackupState
	}{
		{"off", off, &State{LastError: "x"}, false, nil, command.BackupOff},
		{"running", sched, &State{LastError: "x"}, true, nil, command.BackupRunning},
		{"failing", sched, &State{LastError: "x", LastSuccess: ago(time.Minute)}, false, nil, command.BackupFailing},
		{"never", sched, &State{EnabledAt: ago(time.Hour)}, false, nil, command.BackupNever},
		{"never, no state", sched, nil, false, nil, command.BackupNever},
		{"late, never run", sched, &State{EnabledAt: ago(3 * time.Hour)}, false, nil, command.BackupLate},
		{"ok", sched, &State{LastSuccess: ago(90 * time.Minute)}, false, nil, command.BackupOK},
		{"late", sched, &State{LastSuccess: ago(121 * time.Minute)}, false, nil, command.BackupLate},
		{"manual never", manual, &State{EnabledAt: ago(1000 * time.Hour)}, false, nil, command.BackupNever},
		{"manual ok forever", manual, &State{LastSuccess: ago(1000 * time.Hour)}, false, nil, command.BackupOK},
		{"custom running", custom, nil, false, up, command.BackupOK},
		{"custom stopped", custom, nil, false, func(string) bool { return false }, command.BackupOff},
		{"custom unknown", custom, nil, false, nil, command.BackupOff},
	} {
		if got := state(tc.b, tc.st, tc.running, now, tc.svc); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestDecorate(t *testing.T) {
	e := newEngine(planned("app-1", "replica", recipe.TypeCustom, true, ""), planned("app-1", "db", recipe.TypeSQLite, false, "1h"))
	s, clock, _ := start(t, e)
	idle(t, clock)
	apps := s.Decorate([]command.AppStatus{
		{AppID: "app-1", Containers: []command.ContainerStatus{{Name: "wf-app-1-svc-1", StatusCode: command.ContainerStatusActive}}},
		{AppID: "app-2"},
	}, clock.Now())
	if len(apps[0].Backups) != 2 || apps[0].Backups[0].State != command.BackupOK || apps[0].Backups[1].State != command.BackupOff || apps[1].Backups != nil {
		t.Fatalf("apps = %+v", apps)
	}
	apps[0].Containers[0].StatusCode = command.ContainerStatusStopped
	if got := s.Decorate(apps, clock.Now()); got[0].Backups[0].State != command.BackupOff {
		t.Fatalf("stopped service = %+v", got[0].Backups)
	}
}

// Once Run has returned nothing would answer a queued request: Back up now
// fails at once instead of waiting for a run that never comes.
func TestRunNowAfterStop(t *testing.T) {
	s := New(newEngine(), t.TempDir(), logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Run(ctx)
	select {
	case o := <-s.RunNow("app-1", "db"):
		if !errors.Is(o.Err, errStopped) {
			t.Fatalf("outcome = %+v", o)
		}
	case <-time.After(time.Second):
		t.Fatal("RunNow after Run returned never answered")
	}
}

func quietLog() *logger.Logger {
	return logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
}

// startOn runs a scheduler over an existing data dir.
func startOn(t *testing.T, e Engine, dir string, opts ...func(*Scheduler)) (*Scheduler, *fakeClock) {
	t.Helper()
	s := New(e, dir, quietLog())
	clock := newClock()
	s.clock = clock
	for _, o := range opts {
		o(s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s, clock
}

// Forget returns only once the run it stopped has returned, so the caller
// can remove the app's volumes; the wait is bounded when the run never does.
func TestForgetWaitsForTheRun(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, recipe.Manual))
	e.block, e.exitDelay = make(chan struct{}), 150*time.Millisecond
	s, clock, _ := start(t, e)
	idle(t, clock)
	out := s.RunNow("app-1", "db")
	ran(t, e)
	s.Forget("app-1")
	if got := e.exits.Load(); got != 1 {
		t.Fatalf("Forget returned before the run did (exits = %d)", got)
	}
	if o := <-out; o.Err == nil {
		t.Fatalf("outcome = %+v", o)
	}
}

func TestForgetWaitIsBounded(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, recipe.Manual))
	e.block, e.ignore = make(chan struct{}), true
	defer close(e.block)
	s, clock, _ := start(t, e, func(s *Scheduler) { s.grace = 50 * time.Millisecond })
	idle(t, clock)
	s.RunNow("app-1", "db")
	ran(t, e)
	begin := time.Now()
	s.Forget("app-1")
	if d := time.Since(begin); d > 5*time.Second || e.exits.Load() != 0 {
		t.Fatalf("Forget took %s, exits = %d", d, e.exits.Load())
	}
}

// A run that has just become the running job already has its cancel, so a
// Forget landing between next and the engine call cannot miss it.
func TestForgetCancelsAJobThatJustStarted(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, recipe.Manual))
	s := New(e, t.TempDir(), quietLog())
	s.grace = 10 * time.Millisecond
	s.RunNow("app-1", "db")
	j := s.next(context.Background(), t0)
	if j == nil || j.ctx == nil || j.cancel == nil {
		t.Fatalf("job = %+v", j)
	}
	s.Forget("app-1")
	if j.ctx.Err() == nil {
		t.Fatal("Forget did not cancel the job that had just started")
	}
}

// A run that outlives its grace blocks its own app until it returns: Back up
// now fails at once and nothing is scheduled for the app, while other apps
// run on.
func TestAbandonedRunBlocksItsApp(t *testing.T) {
	e := newEngine(
		planned("app-1", "db", recipe.TypeSQLite, true, recipe.Manual),
		planned("app-1", "other", recipe.TypeFiles, true, recipe.Manual),
		planned("app-2", "db", recipe.TypeSQLite, true, recipe.Manual))
	e.block, e.ignore, e.blockOnly = make(chan struct{}), true, "app-1"
	s, clock, _ := start(t, e, func(s *Scheduler) { s.runTimeout, s.grace = 20*time.Millisecond, 20*time.Millisecond })
	idle(t, clock)
	if o := <-s.RunNow("app-1", "db"); o.Err == nil || !strings.Contains(o.Err.Error(), "did not stop") {
		t.Fatalf("abandoned = %+v", o)
	}
	ran(t, e)
	if o := <-s.RunNow("app-1", "other"); !errors.Is(o.Err, errStillStopping) || !strings.Contains(o.Err.Error(), "still stopping") {
		t.Fatalf("blocked = %+v", o)
	}
	noRun(t, e)
	if o := <-s.RunNow("app-2", "db"); o.Err != nil {
		t.Fatalf("another app = %+v", o)
	}
	ran(t, e)
	close(e.block)
	deadline := time.Now().Add(5 * time.Second)
	for {
		o := <-s.RunNow("app-1", "other")
		if o.Err == nil {
			break
		}
		if !errors.Is(o.Err, errStillStopping) || time.Now().After(deadline) {
			t.Fatalf("after the run returned: %+v", o)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A scheduled backup of an app with a stuck run waits for it to return.
func TestAbandonedRunHoldsScheduledBackups(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, "5m"))
	e.block, e.ignore = make(chan struct{}), true
	s, clock, _ := start(t, e, func(s *Scheduler) { s.runTimeout, s.grace = 20*time.Millisecond, 20*time.Millisecond })
	idle(t, clock)
	clock.Advance(5 * time.Minute)
	ran(t, e)
	if wait := idle(t, clock); wait != idleWait {
		t.Fatalf("wait with the app stuck = %s", wait)
	}
	s.mu.Lock()
	_, due := s.dueLocked(s.plan[0], clock.Now().Add(time.Hour))
	s.mu.Unlock()
	if due {
		t.Fatal("a stuck app's backup is still scheduled")
	}
	close(e.block)
	if wait := idle(t, clock); wait != 5*time.Minute {
		t.Fatalf("wait after the run returned = %s", wait)
	}
}

// gateEngine holds one plan read open so a Forget can land inside it.
type gateEngine struct {
	*fakeEngine
	gate    atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (g *gateEngine) BackupPlan() ([]dockercompose.PlannedBackup, error) {
	plan, err := g.fakeEngine.BackupPlan()
	if g.gate.Load() {
		g.entered <- struct{}{}
		<-g.release
	}
	return plan, err
}

// A Forget during a plan read sticks: the stale plan read does not bring the
// deleted app, or its state, back.
func TestForgetDuringPlanRead(t *testing.T) {
	e := &gateEngine{fakeEngine: newEngine(planned("app-1", "db", recipe.TypeSQLite, true, "5m")),
		entered: make(chan struct{}), release: make(chan struct{})}
	dir := t.TempDir()
	s, clock := startOn(t, e, dir)
	idle(t, clock)
	e.gate.Store(true)
	s.Reload()
	<-e.entered
	e.setPlan() // the app is gone by the next read
	s.Forget("app-1")
	e.gate.Store(false)
	e.release <- struct{}{}
	idle(t, clock)
	idle(t, clock)
	s.mu.Lock()
	state, plan, forgotten := len(s.state), len(s.plan), len(s.forgotten)
	s.mu.Unlock()
	raw, _ := os.ReadFile(filepath.Join(dir, "backups", "state.json"))
	if state != 0 || plan != 0 || forgotten != 0 || strings.Contains(string(raw), "app-1") {
		t.Fatalf("state %d, plan %d, forgotten %d, file %s", state, plan, forgotten, raw)
	}
}

// A recorded time after now (the clock stepped back) counts as now: the
// backup is not held off until the clock catches up.
func TestFutureTimestampsCountAsNow(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "backups"), 0o700); err != nil {
		t.Fatal(err)
	}
	future := t0.Add(1000 * time.Hour)
	raw, _ := json.Marshal(map[string]State{
		"app-1/db":    {LastAttempt: future, LastSuccess: future, EnabledAt: t0},
		"app-1/files": {EnabledAt: future},
	})
	if err := os.WriteFile(filepath.Join(dir, "backups", "state.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, "5m"))
	_, clock := startOn(t, e, dir)
	if wait := idle(t, clock); wait != 5*time.Minute {
		t.Fatalf("wait = %s, want 5m from now", wait)
	}
	clock.Advance(5 * time.Minute)
	ran(t, e)

	f := newEngine(planned("app-1", "files", recipe.TypeFiles, true, "1h"))
	_, clock = startOn(t, f, dir)
	if wait, max := idle(t, clock), Offset("app-1", "files", time.Hour); wait != max {
		t.Fatalf("never-run wait = %s, want the offset %s from now", wait, max)
	}
}

// Run's exit answers the requests still queued.
func TestStopAnswersQueuedRequests(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, recipe.Manual), planned("app-2", "db", recipe.TypeSQLite, true, recipe.Manual))
	e.block = make(chan struct{})
	s := New(e, t.TempDir(), quietLog())
	clock := newClock()
	s.clock = clock
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	idle(t, clock)
	running := s.RunNow("app-1", "db")
	ran(t, e)
	queued := s.RunNow("app-2", "db")
	cancel()
	<-done
	if o := <-queued; !errors.Is(o.Err, errStopped) {
		t.Fatalf("queued = %+v", o)
	}
	if o := <-running; o.Err == nil {
		t.Fatalf("running = %+v", o)
	}
}

// Forgetting one app leaves the other apps' queued requests alone.
func TestForgetKeepsOtherAppsQueued(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, recipe.Manual), planned("app-2", "db", recipe.TypeSQLite, true, recipe.Manual))
	e.block, e.blockOnly = make(chan struct{}), "app-1"
	s, clock, _ := start(t, e)
	idle(t, clock)
	s.RunNow("app-1", "db")
	ran(t, e)
	other := s.RunNow("app-2", "db")
	s.Forget("app-1")
	if j := ran(t, e); j.AppID != "app-2" {
		t.Fatalf("run = %+v", j)
	}
	if o := <-other; o.Err != nil {
		t.Fatalf("other app's request = %+v", o)
	}
}

// A custom backup is running when its own service is, not a service whose
// name merely starts with it.
func TestCustomHealthMatchesTheExactService(t *testing.T) {
	e := newEngine(planned("app-1", "replica", recipe.TypeCustom, true, ""))
	e.plan[0].Service = "db"
	s, clock, _ := start(t, e)
	idle(t, clock)
	apps := []command.AppStatus{{AppID: "app-1", Containers: []command.ContainerStatus{
		{Name: "wf-app-1-db-replica-1", StatusCode: command.ContainerStatusActive}}}}
	if got := s.Decorate(apps, clock.Now()); got[0].Backups[0].State != command.BackupOff {
		t.Fatalf("db-replica counted as db: %+v", got[0].Backups)
	}
	apps[0].Containers = append(apps[0].Containers, command.ContainerStatus{Name: "wf-app-1-db-2", StatusCode: command.ContainerStatusActive})
	if got := s.Decorate(apps, clock.Now()); got[0].Backups[0].State != command.BackupOK {
		t.Fatalf("db not counted: %+v", got[0].Backups)
	}
}
