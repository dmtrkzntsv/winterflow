package dockercompose

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// appBusyWait bounds how long a public mutator waits for an app's lock.
// The standalone InProcessBridge drains every command on one goroutine, so
// waiting out a shared-variable reload (up to 10 minutes) would wedge all
// app management; the caller gets ErrAppBusy and retries instead.
const appBusyWait = 30 * time.Second

// ErrAppBusy is returned by a mutator that could not take the app's lock
// within the busy deadline (another operation, e.g. a shared-variable
// reload, holds it).
var ErrAppBusy = errors.New("is busy with another operation, try again")

// appSem returns appID's lock: a one-slot semaphore, so a wait can be
// bounded by a deadline and a ctx.
func (r *Repository) appSem(appID string) chan struct{} {
	r.lockMu.Lock()
	defer r.lockMu.Unlock()
	sem, ok := r.appLocks[appID]
	if !ok {
		sem = make(chan struct{}, 1)
		r.appLocks[appID] = sem
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
		return nil, fmt.Errorf("app %s %w", appID, ErrAppBusy)
	}
}

// lockForMutation takes appID's lock for a public mutator (save, start,
// stop, restart, update, delete, rollback, rename, source auto-update),
// held for its whole run, giving up after r.busyWait.
func (r *Repository) lockForMutation(ctx context.Context, appID string) (func(), error) {
	return r.acquireApp(ctx, appID, r.busyWait)
}

// lockApp takes appID's lock, waiting as long as it takes.
func (r *Repository) lockApp(appID string) func() {
	release, _ := r.acquireApp(context.Background(), appID, 0)
	return release
}
