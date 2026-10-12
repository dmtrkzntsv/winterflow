package dockercompose

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// appBusyWait bounds how long a public mutator waits for an app's lock.
// The standalone InProcessBridge drains every command on one goroutine, so
// waiting out a shared-variable reload (up to 10 minutes) would wedge all
// app management; the caller gets ErrAppBusy and retries instead.
const appBusyWait = 30 * time.Second

// appBusyError is returned by a mutator that could not take the app's
// operation lock within the busy deadline (another operation, e.g. a
// shared-variable reload, holds it). It matches ErrAppBusy.
type appBusyError struct{ appID string }

func (e appBusyError) Error() string {
	return "app " + e.appID + " is busy with another operation, try again"
}

func (e appBusyError) Is(target error) bool { return target == ErrAppBusy }

// The operation lock (opLocks, this file) and the file lock (lockApp,
// lockSource in repository.go) are distinct. Lock order:
//
//	operation lock -> beginOp/beginRestore bookkeeping -> lockApp -> lockSource
//	-> sharedMu -> refMu/staleMu
//
// (A save and a source poll take lockSource and release it before lockApp;
// only a rollback holds both, lockApp first.)
//
// The operation lock is held for a public mutator's whole run (compose
// included) and by the shared-variable reload worker; the file lock only for
// file work and one commit. A restore never takes the operation lock: it is
// excluded from the mutators by beginRestore/beginOp instead.

// appSem returns appID's operation lock: a one-slot semaphore, so a wait can
// be bounded by a deadline and a ctx.
func (r *Repository) appSem(appID string) chan struct{} {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	if r.opLocks == nil {
		r.opLocks = map[string]chan struct{}{}
	}
	sem, ok := r.opLocks[appID]
	if !ok {
		sem = make(chan struct{}, 1)
		r.opLocks[appID] = sem
	}
	return sem
}

// acquireApp takes appID's mutation lock and returns its release. It waits
// until ctx ends and, when limit > 0, at most limit (then ErrAppBusy).
// Internal helpers never take the lock (no re-entry).
func (r *Repository) acquireApp(ctx context.Context, appID string, limit time.Duration) (func(), error) {
	sem := r.appSem(appID)
	var once sync.Once
	release := func() { once.Do(func() { <-sem }) }
	select {
	case sem <- struct{}{}:
		return release, nil
	default:
	}
	var deadline <-chan time.Time
	if limit > 0 {
		t := time.NewTimer(limit)
		defer t.Stop()
		deadline = t.C
	}
	select {
	case sem <- struct{}{}:
		return release, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("app %s: %w", appID, ctx.Err())
	case <-deadline:
		return nil, appBusyError{appID: appID}
	}
}

// lockForMutation takes appID's lock for a public mutator (save, start,
// stop, restart, update, delete, rollback, rename, source auto-update),
// held for its whole run, giving up after r.busyWait (appBusyWait when
// unset).
func (r *Repository) lockForMutation(ctx context.Context, appID string) (func(), error) {
	wait := r.busyWait
	if wait <= 0 {
		wait = appBusyWait
	}
	return r.acquireApp(ctx, appID, wait)
}

// lockAppOp takes appID's operation lock, waiting as long as it takes.
func (r *Repository) lockAppOp(appID string) func() {
	release, _ := r.acquireApp(context.Background(), appID, 0)
	return release
}
