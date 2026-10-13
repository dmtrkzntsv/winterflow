package rollouts

import (
	"context"
	"errors"
	"slices"
	"time"

	"winterflow/internal/domain/model"
)

// runner is one kind's step logic.
type runner interface {
	// next picks the rollout's next app, evaluated fresh each step (an app
	// handled by other means meanwhile is not picked). A pick without an
	// app is the end, or with wait set: nothing yet, look again shortly
	// (the worker gives the lease back and takes other rollouts meanwhile).
	next(ctx context.Context, r model.Rollout) (pick, error)
	// run takes the step on the picked app: dispatch, await the result,
	// wait for the app to report running.
	run(ctx context.Context, r model.Rollout, p pick) stepResult
}

// pick is the app a step works on.
type pick struct {
	appID, serverID string
	// skipID is what a skip records in the rollout's skip list.
	skipID string
	// remaining counts the apps still to do, this one included.
	remaining int64
	// wait: no app yet, but the scope is not settled.
	wait bool
	// inst is a template_upgrade's instance; revSeen the server's
	// shared-variables revision a vars_reload picked the app at.
	inst    model.Instance
	revSeen int64
}

// outcome is how a step ended.
type outcome int

const (
	stepDone outcome = iota
	stepSkipped
	stepFailed
)

type stepResult struct {
	outcome outcome
	err     string
	// forget puts a done step's skipID on the skip list: the app counts as
	// done but must not be picked again.
	forget bool
}

func done() stepResult             { return stepResult{outcome: stepDone} }
func skipped() stepResult          { return stepResult{outcome: stepSkipped} }
func failed(msg string) stepResult { return stepResult{outcome: stepFailed, err: msg} }

// errBadScope fails a rollout whose scope cannot be read.
var errBadScope = errors.New("the rollout's scope is unreadable")

// Run drives the queue until ctx is done: a pass now, then one on every
// Wake and once a minute (the idle cost: one indexed query). While a rollout
// waits for a server (it is parked, see drive) a pass is also due when its
// park ends, settlePoll after the pass that parked it; a Wake in between
// does not move that.
func (uc *UseCase) Run(ctx context.Context) {
	tick := time.NewTicker(uc.t.Idle)
	defer tick.Stop()
	var (
		retryTimer *time.Timer
		retry      <-chan time.Time
	)
	defer func() {
		if retryTimer != nil {
			retryTimer.Stop()
		}
	}()
	for {
		if uc.pass(ctx) {
			if retryTimer != nil {
				retryTimer.Stop()
			}
			retryTimer = time.NewTimer(uc.t.SettlePoll)
			retry = retryTimer.C
		}
		select {
		case <-ctx.Done():
			return
		case <-uc.wake:
		case <-tick.C:
		case <-retry:
			retry = nil
		}
	}
}

// pass claims and drives the claimable rollouts, one at a time, until none
// is left, and reports whether any rollout parked itself to wait for a
// server (the caller looks again when the park ends).
func (uc *UseCase) pass(ctx context.Context) (waiting bool) {
	for ctx.Err() == nil {
		ids, err := uc.d.Repo.ListClaimable(ctx, uc.now())
		if err != nil {
			uc.d.Log.Warn("rollouts: list claimable", "error", err)
			return waiting
		}
		claimed := false
		for _, id := range ids {
			now := uc.now()
			ok, err := uc.d.Repo.Claim(ctx, id, uc.owner, now, now.Add(uc.t.Lease))
			if err != nil {
				uc.d.Log.Warn("rollouts: claim", "rollout_id", id, "error", err)
				return waiting
			}
			if ok {
				claimed = true
				waiting = uc.drive(ctx, id) || waiting
			}
		}
		if !claimed {
			return waiting
		}
	}
	return waiting
}

// drive takes the claimed rollout's steps while this process holds its
// lease. Losing the lease (another process took over after it expired)
// cancels the step in flight; the new owner re-evaluates it. A rollout that
// has nothing to do until a server reports (the kind's wait) is parked and
// drive returns true: sleeping here would hold the one worker (and every
// other rollout, other orgs' too) for as long as the server stays quiet, and
// leaving it claimable would fill the claimable list's limit with waiting
// rollouts. Parked means unowned with a lease that ends settlePoll from now,
// so the claimable list skips it until then. On a graceful shutdown the
// lease is given back outright (claimable at once), so a restarted process
// resumes without waiting for the lease to expire.
func (uc *UseCase) drive(parent context.Context, id string) (waiting bool) {
	ctx, cancel := context.WithCancel(parent)
	renewing := make(chan struct{})
	go func() {
		defer close(renewing)
		uc.keepLease(ctx, cancel, id)
	}()
	defer func() {
		cancel()
		<-renewing
		switch {
		case waiting:
			uc.release(parent, id, uc.now().Add(uc.t.SettlePoll))
		case parent.Err() != nil:
			uc.release(parent, id, time.Time{})
		}
	}()
	for ctx.Err() == nil {
		r, err := uc.d.Repo.GetRollout(ctx, id)
		if err != nil {
			uc.warn(ctx, "rollouts: read the claimed rollout", id, err)
			return false
		}
		if r.LeaseOwner != uc.owner || !r.State.Active() {
			return false
		}
		switch uc.step(ctx, r) {
		case park:
			return true
		case end:
			return false
		}
	}
	return false
}

// release gives this process's lease on id back (parked until until, or
// claimable at once when zero). Its context outlives the drive's: it runs
// when that was cancelled.
func (uc *UseCase) release(ctx context.Context, id string, until time.Time) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyTimeout)
	defer cancel()
	if _, err := uc.d.Repo.ReleaseLease(ctx, id, uc.owner, until); err != nil {
		uc.d.Log.Warn("rollouts: release lease", "rollout_id", id, "error", err)
	}
}

// keepLease renews the lease until ctx ends; a renewal that finds the
// lease gone (taken over, or the rollout ended) cancels the drive. A failed
// write is retried at the next tick: the lease outlives a few.
func (uc *UseCase) keepLease(ctx context.Context, lost context.CancelFunc, id string) {
	t := time.NewTicker(uc.t.Renew)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		ok, err := uc.d.Repo.RenewLease(ctx, id, uc.owner, uc.now().Add(uc.t.Lease))
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			uc.d.Log.Warn("rollouts: renew lease", "rollout_id", id, "error", err)
		case !ok:
			lost()
			return
		}
	}
}

// then is what drive does after a step.
type then int

const (
	// again takes another step.
	again then = iota
	// end stops driving: the rollout ended, the lease is gone, or an error
	// a later pass retries.
	end
	// park gives the lease back: the rollout has nothing to do until a
	// server reports.
	park
)

// step takes one step of r and says what to do next.
func (uc *UseCase) step(ctx context.Context, r model.Rollout) then {
	if r.StopRequested {
		uc.finish(ctx, r, model.RolloutStopped, "", "", "")
		return end
	}
	k, ok := uc.kinds[r.Kind]
	if !ok {
		uc.finish(ctx, r, model.RolloutFailed, "unknown rollout kind "+r.Kind, "", "")
		return end
	}
	p, err := k.next(ctx, r)
	switch {
	case errors.Is(err, errBadScope):
		uc.finish(ctx, r, model.RolloutFailed, err.Error(), "", "")
		return end
	case err != nil:
		// A database error: the lease expires and a pass retries.
		uc.warn(ctx, "rollouts: pick the next app", r.ID, err)
		return end
	}
	if p.appID == "" {
		if p.wait {
			return park
		}
		ok, err := uc.d.Repo.Complete(ctx, r.ID, uc.owner, r.ScopeRev)
		if err != nil {
			uc.d.Log.Warn("rollouts: complete", "rollout_id", r.ID, "error", err)
			return end
		}
		if ok {
			uc.ended(ctx, r.ID)
			return end
		}
		return again // the scope was extended meanwhile: look again
	}
	total := r.Done + r.Skipped + p.remaining
	if ok, err := uc.d.Repo.StartStep(ctx, r.ID, uc.owner, p.appID, p.serverID, total); err != nil || !ok {
		uc.warn(ctx, "rollouts: start the step", r.ID, err)
		return end
	}
	name := uc.appName(ctx, p.appID)
	uc.changed(ctx, r.ID, p.appID, name)
	res := k.run(ctx, r, p)
	if ctx.Err() != nil {
		return end // the lease is gone or the process stops: the step is redone
	}
	if res.outcome == stepFailed {
		uc.finish(ctx, r, model.RolloutFailed, res.err, p.appID, p.serverID)
		return end
	}
	doneN, skippedN, skipIDs := int64(1), int64(0), r.SkipIDs
	if res.outcome == stepSkipped {
		doneN, skippedN = 0, 1
	}
	if res.outcome == stepSkipped || res.forget {
		skipIDs = append(slices.Clone(r.SkipIDs), p.skipID)
	}
	if ok, err := uc.d.Repo.CountStep(ctx, r.ID, uc.owner, doneN, skippedN, skipIDs); err != nil || !ok {
		uc.warn(ctx, "rollouts: count the step", r.ID, err)
		return end
	}
	uc.changed(ctx, r.ID, p.appID, name)
	return again
}

// finish ends r stopped or failed.
func (uc *UseCase) finish(ctx context.Context, r model.Rollout, state model.RolloutState, msg, appID, serverID string) {
	ok, err := uc.d.Repo.Finish(ctx, r.ID, uc.owner, state, msg, appID, serverID)
	if err != nil {
		uc.d.Log.Warn("rollouts: finish", "rollout_id", r.ID, "error", err)
		return
	}
	if ok {
		uc.ended(ctx, r.ID)
	}
}

// ended announces a rollout that just ended and prunes its scope. Its
// context outlives the drive's: the lease renewal cancels the drive as soon
// as it sees the rollout ended.
func (uc *UseCase) ended(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyTimeout)
	defer cancel()
	r := uc.changed(ctx, id, "", "")
	if r.ID == "" {
		return
	}
	if err := uc.d.Repo.Prune(ctx, r.Kind, r.ScopeKey, keepEnded); err != nil {
		uc.d.Log.Warn("rollouts: prune", "rollout_id", id, "error", err)
	}
}

// changed announces the rollout's new counts or state to its org. name is
// appID's name when the caller has it (a step's app: looked up once per
// step); any other app in flight is looked up.
func (uc *UseCase) changed(ctx context.Context, id, appID, name string) model.Rollout {
	r, err := uc.d.Repo.GetRollout(ctx, id)
	if err != nil {
		uc.warn(ctx, "rollouts: read for an event", id, err)
		return model.Rollout{}
	}
	if appID == "" || r.CurrentAppID != appID {
		name = uc.appName(ctx, r.CurrentAppID)
	}
	uc.publish(ctx, r.OrgID, viewOf(r, name))
	return r
}

// warn logs a worker database error. Nothing is logged without an error
// (a lost race: the lease or the rollout moved on), for a rollout that is
// gone (deleted with its template), or once ctx ended (the lease was lost
// or the process stops: the error is that, not the database).
func (uc *UseCase) warn(ctx context.Context, msg, id string, err error) {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, model.ErrRolloutNotFound) {
		return
	}
	uc.d.Log.Warn(msg, "rollout_id", id, "error", err)
}
