package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/logger"
)

// Reloader is the orchestrator's shared-variable reload surface.
type Reloader interface {
	ReloadCandidates(ctx context.Context, req command.ReloadVarsRequest) ([]string, error)
	// AppExists reports whether the app is on this server (no fork).
	AppExists(appID string) bool
	// ReloadSharedVars reloads one app. skipped reports an app that was no
	// longer running when its turn came: nothing was done, nothing failed.
	ReloadSharedVars(ctx context.Context, appID string) (names []string, skipped bool, err error)
}

// reloadTimeout bounds one app's reload (render + compose up, which may
// pull images).
const reloadTimeout = 10 * time.Minute

// candidatesTimeout bounds one candidate lookup (a `docker ps`), so a hung
// Docker daemon fails the pass instead of wedging the worker forever.
const candidatesTimeout = 2 * time.Minute

// errAppDeleted ends the wait of an app deleted before its reload ran.
var errAppDeleted = errors.New("the app was deleted")

// ReloadOutcome is one waited app's reload result (EnqueueApp). Skipped:
// the app was not running when its turn came, so nothing was reloaded and
// nothing failed; Err names the change that failed to apply.
type ReloadOutcome struct {
	Names   []string
	Skipped bool
	Err     error
}

// ReloadWorker performs vars.reload work on one goroutine, one app at a
// time, so the dispatcher (and standalone's single bridge goroutine) only
// queues it. Requests arriving mid-pass are merged and re-evaluated after
// the current app; an app already queued is not queued twice.
type ReloadWorker struct {
	r       Reloader
	sink    EventSink
	log     *logger.Logger
	timeout time.Duration
	// candidatesTimeout bounds each ReloadCandidates call.
	candidatesTimeout time.Duration
	wake              chan struct{}

	mu      sync.Mutex
	pending reloadBatch
	// busy is true while a pass runs; only then can a forgotten app still
	// be reached (queued, running or being resolved), so only then is it
	// recorded in dropped. Both are reset when the pass ends.
	busy    bool
	dropped map[string]bool
	// waiters are the single-app requests waiting for an app's next
	// reload; a reload takes those registered before it starts.
	waiters map[string][]chan ReloadOutcome
}

// reloadBatch is merged pending work: all (every stale running app),
// names (stale apps using them) and explicit app ids. waitedOnly marks a
// taken batch of explicit app ids that each have a waiter (the single-app
// form).
type reloadBatch struct {
	all        bool
	names      map[string]bool
	apps       map[string]bool
	waitedOnly bool
}

func newBatch() reloadBatch {
	return reloadBatch{names: map[string]bool{}, apps: map[string]bool{}}
}

func (b reloadBatch) empty() bool { return !b.all && len(b.names) == 0 && len(b.apps) == 0 }

// NewReloadWorker builds a worker reporting each app's outcome as a
// vars.reloaded event through sink. Start it with Run.
func NewReloadWorker(r Reloader, sink EventSink, log *logger.Logger) *ReloadWorker {
	return &ReloadWorker{
		r: r, sink: sink, log: log, timeout: reloadTimeout, candidatesTimeout: candidatesTimeout,
		wake: make(chan struct{}, 1), pending: newBatch(), dropped: map[string]bool{},
		waiters: map[string][]chan ReloadOutcome{},
	}
}

// Enqueue merges req into the pending work and wakes the worker. It never
// blocks.
func (w *ReloadWorker) Enqueue(req command.ReloadVarsRequest) {
	w.mu.Lock()
	switch {
	case len(req.AppIDs) > 0:
		for _, id := range req.AppIDs {
			w.pending.apps[id] = true
			delete(w.dropped, id)
		}
	case len(req.Names) > 0:
		for _, n := range req.Names {
			w.pending.names[n] = true
		}
	default:
		w.pending.all = true
	}
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// EnqueueApp queues appID like Enqueue with that one app id and returns the
// channel its outcome arrives on, once: the outcome of the app's next
// reload, a skip when it is not running then, or an error when it is
// deleted first or the worker stops. It never blocks.
func (w *ReloadWorker) EnqueueApp(appID string) <-chan ReloadOutcome {
	ch := make(chan ReloadOutcome, 1)
	w.mu.Lock()
	w.waiters[appID] = append(w.waiters[appID], ch)
	w.mu.Unlock()
	w.Enqueue(command.ReloadVarsRequest{AppIDs: []string{appID}})
	return ch
}

// takeWaiters removes and returns appID's waiters.
func (w *ReloadWorker) takeWaiters(appID string) []chan ReloadOutcome {
	w.mu.Lock()
	defer w.mu.Unlock()
	ws := w.waiters[appID]
	delete(w.waiters, appID)
	return ws
}

// deliver sends o to each waiter (buffered, each used once: never blocks).
func deliver(ws []chan ReloadOutcome, o ReloadOutcome) {
	for _, ch := range ws {
		ch <- o
	}
}

// Forget drops a deleted app's queued work and ends its waits.
func (w *ReloadWorker) Forget(appID string) {
	w.mu.Lock()
	delete(w.pending.apps, appID)
	if w.busy {
		w.dropped[appID] = true
	}
	w.mu.Unlock()
	deliver(w.takeWaiters(appID), ReloadOutcome{Err: errAppDeleted})
}

// Run processes work until ctx is done; then every wait still open ends
// with ctx's error.
func (w *ReloadWorker) Run(ctx context.Context) {
	defer func() {
		w.mu.Lock()
		all := w.waiters
		w.waiters = map[string][]chan ReloadOutcome{}
		w.mu.Unlock()
		for _, ws := range all {
			deliver(ws, ReloadOutcome{Err: ctx.Err()})
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
			w.drain(ctx)
		}
	}
}

// drain runs one pass: it takes the pending work, reloads the candidate
// apps one at a time and, after each app, merges whatever arrived meanwhile.
func (w *ReloadWorker) drain(ctx context.Context) {
	w.mu.Lock()
	w.busy = true
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.busy = false
		w.dropped = map[string]bool{}
		w.mu.Unlock()
	}()

	var queue []string
	queued := map[string]bool{}
	for {
		if b, ok := w.take(); ok {
			for _, id := range w.resolve(ctx, b) {
				if !queued[id] {
					queued[id] = true
					queue = append(queue, id)
				}
			}
		}
		if len(queue) == 0 || ctx.Err() != nil {
			return
		}
		appID := queue[0]
		queue = queue[1:]
		// Unmarked before it runs: a request that arrives during this
		// reload (e.g. explicit app_ids for this very app, after another
		// change) queues it again on purpose, since it must render the
		// newer snapshot.
		delete(queued, appID)
		if w.isDropped(appID) {
			continue
		}
		w.reloadOne(ctx, appID)
	}
}

func (w *ReloadWorker) take() (reloadBatch, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	b := w.pending
	if b.empty() {
		return b, false
	}
	w.pending = newBatch()
	b.waitedOnly = !b.all && len(b.names) == 0
	for id := range b.apps {
		b.waitedOnly = b.waitedOnly && len(w.waiters[id]) > 0
	}
	return b, true
}

func (w *ReloadWorker) isDropped(appID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dropped[appID]
}

// resolve is the batch's apps to reload, in order. A batch of waited app
// ids only (the single-app form) skips the candidate lookup (a full docker
// ps): ReloadSharedVars checks under the app's lock that the app runs and
// reports a stopped one skipped; one gone from this server is skipped here,
// as the lookup would. Any other batch takes the lookup.
func (w *ReloadWorker) resolve(ctx context.Context, b reloadBatch) []string {
	if !b.waitedOnly {
		return w.candidates(ctx, b)
	}
	var out []string
	for _, id := range sortedKeys(b.apps) {
		if w.r.AppExists(id) {
			out = append(out, id)
		} else {
			deliver(w.takeWaiters(id), ReloadOutcome{Skipped: true})
		}
	}
	return out
}

// candidates resolves a batch to app ids, in request order, without
// duplicates.
func (w *ReloadWorker) candidates(ctx context.Context, b reloadBatch) []string {
	var reqs []command.ReloadVarsRequest
	if b.all {
		reqs = append(reqs, command.ReloadVarsRequest{})
	} else if len(b.names) > 0 {
		reqs = append(reqs, command.ReloadVarsRequest{Names: sortedKeys(b.names)})
	}
	if len(b.apps) > 0 {
		reqs = append(reqs, command.ReloadVarsRequest{AppIDs: sortedKeys(b.apps)})
	}
	seen := map[string]bool{}
	var out []string
	for _, req := range reqs {
		cctx, cancel := context.WithTimeout(ctx, w.candidatesTimeout)
		ids, err := w.r.ReloadCandidates(cctx, req)
		cancel()
		if err != nil {
			// No app to attribute an outcome to: the request is dropped
			// and the apps stay stale until the next vars.reload. A waited
			// app hears why.
			w.log.Warn("shared variable reload: list candidates failed, request dropped",
				"names", req.Names, "app_ids", req.AppIDs, "error", err)
			for _, id := range req.AppIDs {
				seen[id] = true
				deliver(w.takeWaiters(id), ReloadOutcome{Err: err})
			}
			continue
		}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	// An explicit app the lookup left out is not running (or gone): a
	// waited one is skipped.
	for id := range b.apps {
		if !seen[id] {
			deliver(w.takeWaiters(id), ReloadOutcome{Skipped: true})
		}
	}
	return out
}

// reloadOne reloads appID under the per-app deadline and reports the
// outcome. A skipped app (stopped since it was picked) reports nothing:
// it neither reloaded nor failed, and its next start renders anyway.
func (w *ReloadWorker) reloadOne(ctx context.Context, appID string) {
	// The waits registered before this reload starts hear its outcome; a
	// later one waits for the next reload (it may need a newer snapshot).
	waiting := w.takeWaiters(appID)
	var out ReloadOutcome
	defer func() { deliver(waiting, out) }()
	actx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	names, skipped, err := w.r.ReloadSharedVars(actx, appID)
	out.Names = names
	if ctx.Err() != nil {
		out.Err = ctx.Err()
		return // shutting down: no outcome to report
	}
	if skipped && err == nil {
		w.log.Debug("shared variable reload: app no longer running, skipped", "app_id", appID)
		out.Skipped = true
		return
	}
	p := command.VarsReloadedPayload{AppID: appID, OK: err == nil, Names: names}
	if p.Names == nil {
		p.Names = []string{}
	}
	if err != nil {
		p.Error = reloadError(names, err)
		out.Err = errors.New(p.Error)
		w.log.Warn("shared variable reload failed", "app_id", appID, "error", err)
	}
	body, mErr := json.Marshal(p)
	if mErr != nil {
		w.log.Error("shared variable reload: marshal outcome", "error", mErr)
		return
	}
	if sErr := w.sink(bus.EventVarsReloaded, body); sErr != nil {
		w.log.Debug("shared variable reload: report outcome", "app_id", appID, "error", sErr)
	}
}

// reloadError names the variable change that triggered a failed reload.
func reloadError(names []string, err error) string {
	if len(names) == 0 {
		return "reload after a shared variable change failed: " + err.Error()
	}
	return "reload after a change to " + strings.Join(names, ", ") + " failed: " + err.Error()
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
