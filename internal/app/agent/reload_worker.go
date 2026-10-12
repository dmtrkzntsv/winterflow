package agent

import (
	"context"
	"encoding/json"
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
}

// reloadBatch is merged pending work: all (every stale running app),
// names (stale apps using them) and explicit app ids.
type reloadBatch struct {
	all   bool
	names map[string]bool
	apps  map[string]bool
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

// Forget drops a deleted app's queued work.
func (w *ReloadWorker) Forget(appID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.pending.apps, appID)
	if w.busy {
		w.dropped[appID] = true
	}
}

// Run processes work until ctx is done.
func (w *ReloadWorker) Run(ctx context.Context) {
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
			for _, id := range w.candidates(ctx, b) {
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
	return b, true
}

func (w *ReloadWorker) isDropped(appID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dropped[appID]
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
			// and the apps stay stale until the next vars.reload.
			w.log.Warn("shared variable reload: list candidates failed, request dropped",
				"names", req.Names, "app_ids", req.AppIDs, "error", err)
			continue
		}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// reloadOne reloads appID under the per-app deadline and reports the
// outcome. A skipped app (stopped since it was picked) reports nothing:
// it neither reloaded nor failed, and its next start renders anyway.
func (w *ReloadWorker) reloadOne(ctx context.Context, appID string) {
	actx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	names, skipped, err := w.r.ReloadSharedVars(actx, appID)
	if ctx.Err() != nil {
		return // shutting down: no outcome to report
	}
	if skipped && err == nil {
		w.log.Debug("shared variable reload: app no longer running, skipped", "app_id", appID)
		return
	}
	p := command.VarsReloadedPayload{AppID: appID, OK: err == nil, Names: names}
	if p.Names == nil {
		p.Names = []string{}
	}
	if err != nil {
		p.Error = reloadError(names, err)
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
