// Package backup is the agent's backup scheduler: one goroutine, next to the
// source poller and independent of the command pipeline, that runs every
// enabled sqlite and files backup on its schedule and the Back up now
// requests, strictly one run at a time. It wakes on a single timer set to
// the earliest due time, keeps each backup's last attempt and success in
// {AGENT_DATA_DIR}/backups/state.json, and reports each backup's health for
// apps.status.
package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/pkg/fsutil"
	"winterflow/pkg/logger"
)

// Engine runs backups: the Docker Compose orchestrator.
type Engine interface {
	BackupPlan() ([]dockercompose.PlannedBackup, error)
	RunBackup(ctx context.Context, job dockercompose.BackupJob) (dockercompose.BackupResult, error)
}

// Clock is the scheduler's time (a fake in tests).
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Limits of one run.
const (
	// RunTimeout bounds a run: the dump and the upload.
	RunTimeout = 30 * time.Minute
	// abandonAfter is how long past its deadline a run gets to stop before
	// the scheduler gives up on it (a storage call stuck on a dead mount)
	// and moves on.
	abandonAfter = time.Minute
	// idleWait is the timer when nothing is scheduled.
	idleWait = 24 * time.Hour
	// tombstoneTTL is how long a Forget keeps its app blocked if no plan
	// read has dropped the app by then (a delete that failed and left the
	// app in place).
	tombstoneTTL = 10 * time.Minute
)

// State is one backup's record in state.json.
type State struct {
	LastAttempt time.Time `json:"last_attempt,omitzero"`
	LastSuccess time.Time `json:"last_success,omitzero"`
	LastError   string    `json:"last_error,omitempty"`
	LastSize    int64     `json:"last_size,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	EnabledAt   time.Time `json:"enabled_at,omitzero"`
}

// Outcome is what a Back up now request learns when its run ends.
type Outcome struct {
	Result command.RunBackupResponse
	Err    error
}

type key struct{ app, name string }

func (k key) String() string { return k.app + "/" + k.name }

// tomb marks an app being deleted: the Forget it came from, and when it lapses.
type tomb struct {
	gen   uint64
	until time.Time
}

// job is one run, waited for by the requests that joined it. Once it is the
// running job its context is bound to run's deadline, and cancel and exited
// are set, all under the scheduler's lock: a Forget either sees the job as
// running (and can stop and await it) or the job has not started.
type job struct {
	key     key
	kind    command.PointKind
	waiters []chan Outcome
	ctx     context.Context
	cancel  context.CancelFunc
	awaited bool // a Forget already waited out its grace on this job
	// exited is closed (under the lock) when the engine's RunBackup call
	// has returned, however long after the run was abandoned.
	exited chan struct{}
}

// Scheduler runs backups. Its methods are safe for concurrent use; Run is
// the one goroutine that runs them.
type Scheduler struct {
	engine     Engine
	path       string
	log        *logger.Logger
	clock      Clock
	runTimeout time.Duration
	grace      time.Duration

	wake chan struct{}

	mu      sync.Mutex
	plan    []dockercompose.PlannedBackup
	state   map[string]*State
	dirty   bool // the plan must be reread
	queue   []*job
	running *job
	// stuck holds, per app, a run that was abandoned past its grace and whose
	// RunBackup call has not returned yet. The app starts no other run
	// until it does (a hung storage call can still be holding its volumes or
	// helper), which also bounds the stuck goroutines to one per app. The
	// global one-run-at-a-time slot is released on abandon, so one dead
	// storage cannot wedge every other app's backups.
	stuck map[string]*job
	// gen counts Forgets; forgotten maps an app to the gen of its last one,
	// so a plan read that began before it cannot bring the app back.
	gen       uint64
	forgotten map[string]uint64
	// deleting blocks a Forgotten app (no run, scheduled or requested) from
	// the first Forget until a plan read no longer lists it, or the TTL.
	deleting map[string]tomb
	stopped  bool // Run has returned: nothing answers a queued request any more
}

// New makes a scheduler keeping its state in dataDir/backups/state.json.
func New(engine Engine, dataDir string, log *logger.Logger) *Scheduler {
	s := &Scheduler{
		engine: engine, path: filepath.Join(dataDir, "backups", "state.json"), log: log,
		clock: realClock{}, runTimeout: RunTimeout, grace: abandonAfter,
		wake: make(chan struct{}, 1), state: map[string]*State{}, dirty: true,
		stuck: map[string]*job{}, forgotten: map[string]uint64{}, deleting: map[string]tomb{},
	}
	s.load()
	return s
}

// load reads state.json; a missing or unreadable file is an empty state.
func (s *Scheduler) load() {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var st map[string]*State
	if err := json.Unmarshal(raw, &st); err != nil {
		s.log.Warn("backup state unreadable, starting afresh", "error", err)
		return
	}
	for k, v := range st {
		if v != nil {
			s.state[k] = v
		}
	}
}

// saveLocked writes state.json atomically. The caller holds mu.
func (s *Scheduler) saveLocked() {
	raw, err := json.MarshalIndent(s.state, "", "  ")
	if err == nil {
		if err = os.MkdirAll(filepath.Dir(s.path), 0o700); err == nil {
			err = fsutil.WriteFile(s.path, raw, 0o600)
		}
	}
	if err != nil {
		s.log.Warn("save backup state", "error", err)
	}
}

func (s *Scheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Reload rereads the plan before the next decision: an app was saved,
// rolled back, renamed or deleted, its backups changed, or a storage was
// saved or deleted.
func (s *Scheduler) Reload() {
	s.mu.Lock()
	s.dirty = true
	s.mu.Unlock()
	s.signal()
}

// Forget drops a deleted app: its queued requests fail, a run in progress is
// stopped, and its state is pruned. It returns once the stopped run (or one
// abandoned earlier) has really returned, so the caller can remove the app's
// volumes without racing the backup helper; the wait is bounded by the
// abandon grace, as the caller is the single command goroutine.
func (s *Scheduler) Forget(appID string) {
	s.mu.Lock()
	kept := s.queue[:0]
	for _, j := range s.queue {
		if j.key.app == appID {
			finish(j, Outcome{Err: errors.New("the app was deleted")})
			continue
		}
		kept = append(kept, j)
	}
	s.queue = kept
	var wait []*job
	if r := s.running; r != nil && r.key.app == appID {
		r.cancel()
		wait = append(wait, r)
	}
	// A job already stuck (or waited out by an earlier Forget) has had its
	// grace: waiting again would only stall the caller.
	wait = slices.DeleteFunc(wait, func(j *job) bool { return j.awaited })
	plan := s.plan[:0]
	for _, p := range s.plan {
		if p.AppID != appID {
			plan = append(plan, p)
		}
	}
	s.plan = plan
	s.gen++
	s.forgotten[appID] = s.gen
	s.deleting[appID] = tomb{gen: s.gen, until: s.clock.Now().Add(tombstoneTTL)}
	changed := false
	for k := range s.state {
		if app, _, _ := strings.Cut(k, "/"); app == appID {
			delete(s.state, k)
			changed = true
		}
	}
	if changed {
		s.saveLocked()
	}
	s.dirty = true
	s.mu.Unlock()
	s.signal()

	if len(wait) == 0 {
		return
	}
	timer := time.NewTimer(s.grace)
	defer timer.Stop()
	for _, j := range wait {
		select {
		case <-j.exited:
		case <-timer.C:
			s.mu.Lock()
			for _, w := range wait {
				w.awaited = true
			}
			s.mu.Unlock()
			return
		}
	}
}

// blockedLocked is why an app can start no run now: it is being deleted, or
// an earlier run of it has not stopped. nil when it can.
func (s *Scheduler) blockedLocked(appID string, now time.Time) error {
	if t, ok := s.deleting[appID]; ok && t.until.After(now) {
		return errDeleting
	}
	if s.stuck[appID] != nil {
		return errStillStopping
	}
	return nil
}

// RunNow queues a manual run of an app's backup ahead of the scheduled ones
// and returns where its outcome arrives. A request for a backup that is
// already running or queued fails at once, so waiters
// can't pile up behind one run. Whether the backup can run
// (enabled, sqlite or files) is decided when it runs.
func (s *Scheduler) RunNow(appID, name string) <-chan Outcome {
	ch := make(chan Outcome, 1)
	k := key{appID, name}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		ch <- Outcome{Err: errStopped}
		return ch
	}
	if err := s.blockedLocked(appID, s.clock.Now()); err != nil {
		ch <- Outcome{Err: err}
		return ch
	}
	dup := s.running != nil && s.running.key == k
	for _, j := range s.queue {
		dup = dup || j.key == k
	}
	if dup {
		ch <- Outcome{Err: fmt.Errorf("a backup of %s is already queued or running", name)}
		return ch
	}
	s.queue = append(s.queue, &job{key: k, kind: command.PointManual, waiters: []chan Outcome{ch}})
	s.signal()
	return ch
}

// Run runs backups until ctx is done.
func (s *Scheduler) Run(ctx context.Context) {
	for ctx.Err() == nil {
		s.refresh()
		now := s.clock.Now()
		if j := s.next(ctx, now); j != nil {
			s.execute(ctx, j)
			continue
		}
		select {
		case <-ctx.Done():
		case <-s.clock.After(s.untilDue(now)):
		case <-s.wake:
		}
	}
	s.mu.Lock()
	s.stopped = true
	for _, j := range s.queue {
		finish(j, Outcome{Err: errStopped})
	}
	s.queue = nil
	s.mu.Unlock()
}

var (
	// errStopped answers a request once Run has returned.
	errStopped = errors.New("the agent is stopping")
	// errStillStopping answers a request for an app whose earlier run did
	// not stop within its time limit and has not returned yet.
	errStillStopping = errors.New("a previous backup of this app is still stopping (its storage or Docker is not answering)")
	// errDeleting answers a request for an app that is being deleted.
	errDeleting = errors.New("the app is being deleted")
)

// refresh rereads the plan when asked to, and records when each backup was
// turned on (its first run is offset from then).
func (s *Scheduler) refresh() {
	s.mu.Lock()
	dirty := s.dirty
	s.dirty = false
	readGen := s.gen
	s.mu.Unlock()
	if !dirty {
		return
	}
	plan, err := s.engine.BackupPlan()
	if err != nil {
		s.log.Warn("backup plan: some apps' backups can't be read", "error", err)
	}
	now := s.clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	// A Forget's block ends once a read begun after it no longer lists the
	// app (or at its TTL); a read begun before it proves nothing.
	listed := make(map[string]bool, len(plan))
	for _, p := range plan {
		listed[p.AppID] = true
	}
	for app, t := range s.deleting {
		if t.gen <= readGen && !listed[app] || !t.until.After(now) {
			delete(s.deleting, app)
		}
	}
	// An app forgotten while the plan was being read is not in it again.
	kept := make([]dockercompose.PlannedBackup, 0, len(plan))
	for _, p := range plan {
		if g, ok := s.forgotten[p.AppID]; !ok || g <= readGen {
			kept = append(kept, p)
		}
	}
	plan = kept
	for app, g := range s.forgotten {
		if g <= readGen {
			delete(s.forgotten, app)
		}
	}
	s.plan = plan
	changed := false
	for _, p := range plan {
		k := key{p.AppID, p.Name}.String()
		st := s.state[k]
		switch {
		case p.Enabled && (st == nil || st.EnabledAt.IsZero()):
			if st == nil {
				st = &State{}
				s.state[k] = st
			}
			st.EnabledAt, changed = now, true
		case !p.Enabled && st != nil && !st.EnabledAt.IsZero():
			st.EnabledAt, changed = time.Time{}, true
		}
	}
	if changed {
		s.saveLocked()
	}
}

// next is the job to run now: the oldest Back up now request, else the
// scheduled backup most overdue. An app with a stuck (abandoned) run is
// skipped: its requests fail, its scheduled backups wait.
func (s *Scheduler) next(parent context.Context, now time.Time) *job {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.queue) > 0 {
		j := s.queue[0]
		s.queue = s.queue[1:]
		if err := s.blockedLocked(j.key.app, now); err != nil {
			finish(j, Outcome{Err: err})
			continue
		}
		return s.startLocked(parent, j)
	}
	var (
		best    *dockercompose.PlannedBackup
		bestDue time.Time
	)
	for i, p := range s.plan {
		due, ok := s.dueLocked(p, now)
		if ok && !due.After(now) && (best == nil || due.Before(bestDue)) {
			best, bestDue = &s.plan[i], due
		}
	}
	if best == nil {
		return nil
	}
	return s.startLocked(parent, &job{key: key{best.AppID, best.Name}, kind: command.PointScheduled})
}

// startLocked makes j the running job: its context and cancel exist from
// this moment, under the same lock Forget takes.
func (s *Scheduler) startLocked(parent context.Context, j *job) *job {
	j.ctx, j.cancel = context.WithTimeout(parent, s.runTimeout)
	j.exited = make(chan struct{})
	s.running = j
	return j
}

// dueLocked is when a scheduled backup runs next: its last attempt plus
// every; never run, the time it was turned on plus a stable offset (so
// backups enabled together spread out). ok is false for backups that are
// off, manual or custom, and for apps that are blocked (being deleted, or
// with a stuck run). A recorded time
// after now (the clock stepped back) is taken as now.
func (s *Scheduler) dueLocked(p dockercompose.PlannedBackup, now time.Time) (time.Time, bool) {
	every := p.Interval()
	if !p.Enabled || !p.Builtin() || every <= 0 || s.blockedLocked(p.AppID, now) != nil {
		return time.Time{}, false
	}
	st := s.state[key{p.AppID, p.Name}.String()]
	if st == nil {
		return time.Time{}, false
	}
	st.clamp(now)
	if !st.LastAttempt.IsZero() {
		return st.LastAttempt.Add(every), true
	}
	return st.EnabledAt.Add(Offset(p.AppID, p.Name, every)), true
}

// clamp pulls a recorded time after now (the clock stepped back) down to
// now, for good: left alone it would hold the backup off until the clock
// caught up, and the health would read the future as fine.
func (st *State) clamp(now time.Time) {
	for _, t := range []*time.Time{&st.LastAttempt, &st.LastSuccess, &st.EnabledAt} {
		if t.After(now) {
			*t = now
		}
	}
}

// Offset is a backup's stable delay within its interval: a hash of
// app-id/backup name, mod every.
func Offset(appID, name string, every time.Duration) time.Duration {
	h := fnv.New64a()
	h.Write([]byte(appID + "/" + name))
	return time.Duration(h.Sum64() % uint64(every))
}

// untilDue is how long until the earliest scheduled backup is due.
func (s *Scheduler) untilDue(now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	wait := idleWait
	for app, t := range s.deleting {
		if t.until.After(now) {
			wait = min(wait, t.until.Sub(now))
		} else {
			delete(s.deleting, app)
		}
	}
	for _, p := range s.plan {
		if due, ok := s.dueLocked(p, now); ok {
			wait = min(wait, max(due.Sub(now), 0))
		}
	}
	return wait
}

// execute runs one job within its time limit and records the outcome. A
// run that doesn't stop at its deadline is abandoned after the grace: it is
// recorded as failed, the global slot is released, but its app stays blocked
// until the engine call really returns. A run cut short because the agent is
// stopping (parent is done) is not recorded: see record.
func (s *Scheduler) execute(parent context.Context, j *job) {
	defer j.cancel()
	start := s.clock.Now()
	type done struct {
		res dockercompose.BackupResult
		err error
	}
	ch := make(chan done, 1)
	go func() {
		res, err := s.engine.RunBackup(j.ctx, dockercompose.BackupJob{AppID: j.key.app, Backup: j.key.name, Kind: j.kind})
		ch <- done{res, err}
		s.mu.Lock()
		wasStuck := s.stuck[j.key.app] == j
		if wasStuck {
			delete(s.stuck, j.key.app)
		}
		close(j.exited)
		s.mu.Unlock()
		if wasStuck {
			s.signal() // the app's scheduled backups may run again
		}
	}()
	var d done
	select {
	case d = <-ch:
	case <-j.ctx.Done():
		grace := time.NewTimer(s.grace)
		defer grace.Stop()
		select {
		case d = <-ch:
		case <-grace.C:
			select {
			case d = <-ch:
			default:
				d.err = fmt.Errorf("the run did not stop after its time limit of %s", s.runTimeout)
				s.markStuck(j)
			}
		}
	}
	if d.err == nil && j.ctx.Err() != nil {
		d.err = fmt.Errorf("the run was stopped: %w", j.ctx.Err())
	}
	s.record(j, start, d.res, d.err, d.err != nil && parent.Err() != nil)
}

// markStuck blocks the job's app until its abandoned RunBackup returns (it
// may have returned already).
func (s *Scheduler) markStuck(j *job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-j.exited:
	default:
		s.stuck[j.key.app] = j
	}
}

// record stores a run's outcome as the backup's attempt at start (not for
// an app deleted meanwhile) and answers its waiters. A run refused before
// it started means the plan is stale (an app changed without a Reload, say
// by its upstream): the plan is reread, and only a scheduled run counts as
// an attempt, so it isn't picked again at once. A run that failed because
// the agent is stopping (an update restarts it, say) leaves the state
// alone, so the backup isn't marked failing or held off until its next
// interval: it runs again on start. Its waiters get errStopped.
func (s *Scheduler) record(j *job, start time.Time, res dockercompose.BackupResult, err error, stopping bool) {
	resp := command.RunBackupResponse{AppID: j.key.app, Backup: j.key.name, Unchanged: res.Unchanged, Point: res.Point, Warnings: res.Warnings}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = nil
	if stopping {
		finish(j, Outcome{Result: resp, Err: errStopped})
		return
	}
	if errors.Is(err, dockercompose.ErrNotRunnable) {
		s.dirty = true
		if st := s.state[j.key.String()]; st != nil && j.kind == command.PointScheduled {
			st.LastAttempt = start
			s.saveLocked()
		}
		finish(j, Outcome{Result: resp, Err: err})
		return
	}
	if s.plannedLocked(j.key) {
		st := s.state[j.key.String()]
		if st == nil {
			st = &State{}
			s.state[j.key.String()] = st
		}
		st.LastAttempt = start
		if err != nil {
			st.LastError = err.Error()
			s.log.Warn("backup failed", "app_id", j.key.app, "backup", j.key.name, "error", err)
		} else {
			st.LastError, st.LastSuccess, st.Fingerprint = "", start, res.Fingerprint
			if res.Point != nil {
				st.LastSize = res.Point.Size
			}
		}
		s.saveLocked()
	}
	finish(j, Outcome{Result: resp, Err: err})
}

func (s *Scheduler) plannedLocked(k key) bool {
	for _, p := range s.plan {
		if p.AppID == k.app && p.Name == k.name {
			return true
		}
	}
	return false
}

func finish(j *job, o Outcome) {
	for _, w := range j.waiters {
		w <- o
	}
	j.waiters = nil
}

// Health is the health of every backup of the app at now. running reports
// whether one of the app's services runs (custom backups are ok while
// theirs does).
func (s *Scheduler) Health(appID string, now time.Time, running func(service string) bool) []command.BackupHealth {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []command.BackupHealth
	for _, p := range s.plan {
		if p.AppID != appID {
			continue
		}
		h := command.BackupHealth{Name: p.Name}
		st := s.state[key{p.AppID, p.Name}.String()]
		if st != nil {
			st.clamp(now)
		}
		if st != nil && !st.LastSuccess.IsZero() {
			h.LastSuccess = st.LastSuccess.Unix()
		}
		if st != nil {
			h.LastError = st.LastError
		}
		isRunning := s.running != nil && s.running.key == key{p.AppID, p.Name}
		h.State = state(p.EffectiveBackup, st, isRunning, now, running)
		out = append(out, h)
	}
	return out
}

// state is one backup's health.
func state(b recipe.EffectiveBackup, st *State, running bool, now time.Time, serviceRunning func(string) bool) command.BackupState {
	switch {
	case !b.Enabled:
		return command.BackupOff
	case b.Type == recipe.TypeCustom:
		if serviceRunning != nil && serviceRunning(b.Service) {
			return command.BackupOK
		}
		return command.BackupOff
	case running:
		return command.BackupRunning
	case st != nil && st.LastError != "":
		return command.BackupFailing
	}
	every := b.Interval()
	if st == nil || st.LastSuccess.IsZero() {
		if every > 0 && st != nil && !st.EnabledAt.IsZero() && now.Sub(st.EnabledAt) > 2*every {
			return command.BackupLate
		}
		return command.BackupNever
	}
	if every > 0 && now.Sub(st.LastSuccess) > 2*every {
		return command.BackupLate
	}
	return command.BackupOK
}

// Decorate adds each app's backup health at now to its status. A custom
// backup's service runs when one of the app's containers for it is active.
func (s *Scheduler) Decorate(apps []command.AppStatus, now time.Time) []command.AppStatus {
	for i := range apps {
		a := &apps[i]
		running := func(service string) bool {
			// <prefix><n> exactly: db-replica's containers are not db's.
			prefix := dockercompose.ServiceContainerPrefix(a.AppID, service)
			for _, c := range a.Containers {
				if c.StatusCode == command.ContainerStatusActive && isReplicaOf(c.Name, prefix) {
					return true
				}
			}
			return false
		}
		a.Backups = s.Health(a.AppID, now, running)
	}
	return apps
}

// isReplicaOf reports whether name is prefix followed by a replica number.
func isReplicaOf(name, prefix string) bool {
	n, ok := strings.CutPrefix(name, prefix)
	if !ok || n == "" {
		return false
	}
	for _, r := range n {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
