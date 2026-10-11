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

// job is one run, waited for by the requests that joined it.
type job struct {
	key     key
	kind    command.PointKind
	waiters []chan Outcome
	cancel  context.CancelFunc
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
}

// New makes a scheduler keeping its state in dataDir/backups/state.json.
func New(engine Engine, dataDir string, log *logger.Logger) *Scheduler {
	s := &Scheduler{
		engine: engine, path: filepath.Join(dataDir, "backups", "state.json"), log: log,
		clock: realClock{}, runTimeout: RunTimeout, grace: abandonAfter,
		wake: make(chan struct{}, 1), state: map[string]*State{}, dirty: true,
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
// stopped, and its state is pruned.
func (s *Scheduler) Forget(appID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.queue[:0]
	for _, j := range s.queue {
		if j.key.app == appID {
			finish(j, Outcome{Err: errors.New("the app was deleted")})
			continue
		}
		kept = append(kept, j)
	}
	s.queue = kept
	if s.running != nil && s.running.key.app == appID && s.running.cancel != nil {
		s.running.cancel()
	}
	plan := s.plan[:0]
	for _, p := range s.plan {
		if p.AppID != appID {
			plan = append(plan, p)
		}
	}
	s.plan = plan
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
	s.signal()
}

// RunNow queues a manual run of an app's backup ahead of the scheduled ones
// and returns where its outcome arrives. A request for a backup that is
// already running or queued joins that run. Whether the backup can run
// (enabled, sqlite or files) is decided when it runs.
func (s *Scheduler) RunNow(appID, name string) <-chan Outcome {
	ch := make(chan Outcome, 1)
	k := key{appID, name}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != nil && s.running.key == k {
		s.running.waiters = append(s.running.waiters, ch)
		return ch
	}
	for _, j := range s.queue {
		if j.key == k {
			j.waiters = append(j.waiters, ch)
			return ch
		}
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
		if j := s.next(now); j != nil {
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
	for _, j := range s.queue {
		finish(j, Outcome{Err: errors.New("the agent is stopping")})
	}
	s.queue = nil
	s.mu.Unlock()
}

// refresh rereads the plan when asked to, and records when each backup was
// turned on (its first run is offset from then).
func (s *Scheduler) refresh() {
	s.mu.Lock()
	dirty := s.dirty
	s.dirty = false
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
// scheduled backup most overdue.
func (s *Scheduler) next(now time.Time) *job {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) > 0 {
		j := s.queue[0]
		s.queue = s.queue[1:]
		s.running = j
		return j
	}
	var (
		best    *dockercompose.PlannedBackup
		bestDue time.Time
	)
	for i, p := range s.plan {
		due, ok := s.dueLocked(p)
		if ok && !due.After(now) && (best == nil || due.Before(bestDue)) {
			best, bestDue = &s.plan[i], due
		}
	}
	if best == nil {
		return nil
	}
	s.running = &job{key: key{best.AppID, best.Name}, kind: command.PointScheduled}
	return s.running
}

// dueLocked is when a scheduled backup runs next: its last attempt plus
// every; never run, the time it was turned on plus a stable offset (so
// backups enabled together spread out). ok is false for backups that are
// off, manual or custom.
func (s *Scheduler) dueLocked(p dockercompose.PlannedBackup) (time.Time, bool) {
	every := p.Interval()
	if !p.Enabled || !p.Builtin() || every <= 0 {
		return time.Time{}, false
	}
	st := s.state[key{p.AppID, p.Name}.String()]
	if st == nil {
		return time.Time{}, false
	}
	if !st.LastAttempt.IsZero() {
		return st.LastAttempt.Add(every), true
	}
	return st.EnabledAt.Add(Offset(p.AppID, p.Name, every)), true
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
	for _, p := range s.plan {
		if due, ok := s.dueLocked(p); ok {
			wait = min(wait, max(due.Sub(now), 0))
		}
	}
	return wait
}

// execute runs one job within its time limit and records the outcome. A
// run that doesn't stop at its deadline is abandoned after the grace.
func (s *Scheduler) execute(parent context.Context, j *job) {
	ctx, cancel := context.WithTimeout(parent, s.runTimeout)
	defer cancel()
	start := s.clock.Now()
	s.mu.Lock()
	j.cancel = cancel
	s.mu.Unlock()

	type done struct {
		res dockercompose.BackupResult
		err error
	}
	ch := make(chan done, 1)
	go func() {
		res, err := s.engine.RunBackup(ctx, dockercompose.BackupJob{AppID: j.key.app, Backup: j.key.name, Kind: j.kind})
		ch <- done{res, err}
	}()
	var d done
	select {
	case d = <-ch:
	case <-ctx.Done():
		grace := time.NewTimer(s.grace)
		defer grace.Stop()
		select {
		case d = <-ch:
		case <-grace.C:
			d.err = fmt.Errorf("the run did not stop after its time limit of %s", s.runTimeout)
		}
	}
	if d.err == nil && ctx.Err() != nil {
		d.err = fmt.Errorf("the run was stopped: %w", ctx.Err())
	}
	s.record(j, start, d.res, d.err)
}

// record stores a run's outcome as the backup's attempt at start (not for
// an app deleted meanwhile) and answers its waiters. A run refused before
// it started means the plan is stale (an app changed without a Reload, say
// by its upstream): the plan is reread, and only a scheduled run counts as
// an attempt, so it isn't picked again at once.
func (s *Scheduler) record(j *job, start time.Time, res dockercompose.BackupResult, err error) {
	resp := command.RunBackupResponse{AppID: j.key.app, Backup: j.key.name, Unchanged: res.Unchanged, Point: res.Point, Warnings: res.Warnings}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = nil
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
			prefix := dockercompose.ServiceContainerPrefix(a.AppID, service)
			for _, c := range a.Containers {
				if strings.HasPrefix(c.Name, prefix) && c.StatusCode == command.ContainerStatusActive {
					return true
				}
			}
			return false
		}
		a.Backups = s.Health(a.AppID, now, running)
	}
	return apps
}
