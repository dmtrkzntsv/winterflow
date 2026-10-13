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
	// app is the end, or with wait set: nothing yet, look again shortly.
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
}

func done() stepResult             { return stepResult{outcome: stepDone} }
func skipped() stepResult          { return stepResult{outcome: stepSkipped} }
func failed(msg string) stepResult { return stepResult{outcome: stepFailed, err: msg} }

// errBadScope fails a rollout whose scope cannot be read.
var errBadScope = errors.New("the rollout's scope is unreadable")

// Run drives the queue until ctx is done: a pass now, then one on every
// Wake and once a minute (the idle cost: one indexed query).
func (uc *UseCase) Run(ctx context.Context) {
	tick := time.NewTicker(uc.idle)
	defer tick.Stop()
	for {
		uc.pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-uc.wake:
		case <-tick.C:
		}
	}
}

// pass claims and drives the claimable rollouts, one at a time, until none
// is left.
func (uc *UseCase) pass(ctx context.Context) {
	for ctx.Err() == nil {
		ids, err := uc.d.Repo.ListClaimable(ctx, uc.now())
		if err != nil {
			uc.d.Log.Warn("rollouts: list claimable", "error", err)
			return
		}
		claimed := false
		for _, id := range ids {
			now := uc.now()
			ok, err := uc.d.Repo.Claim(ctx, id, uc.owner, now, now.Add(uc.lease))
			if err != nil {
				uc.d.Log.Warn("rollouts: claim", "rollout_id", id, "error", err)
				return
			}
			if ok {
				claimed = true
				uc.drive(ctx, id)
			}
		}
		if !claimed {
			return
		}
	}
}

// drive takes the claimed rollout's steps while this process holds its
// lease. Losing the lease (another process took over after it expired)
// cancels the step in flight; the new owner re-evaluates it.
func (uc *UseCase) drive(parent context.Context, id string) {
	ctx, cancel := context.WithCancel(parent)
	renewing := make(chan struct{})
	go func() {
		defer close(renewing)
		uc.keepLease(ctx, cancel, id)
	}()
	defer func() {
		cancel()
		<-renewing
	}()
	for ctx.Err() == nil {
		r, err := uc.d.Repo.GetRollout(ctx, id)
		if err != nil || r.LeaseOwner != uc.owner || !r.State.Active() {
			return
		}
		if !uc.step(ctx, r) {
			return
		}
	}
}

// keepLease renews the lease until ctx ends; a renewal that finds the
// lease gone (taken over, or the rollout ended) cancels the drive. A failed
// write is retried at the next tick: the lease outlives a few.
func (uc *UseCase) keepLease(ctx context.Context, lost context.CancelFunc, id string) {
	t := time.NewTicker(uc.renew)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		ok, err := uc.d.Repo.RenewLease(ctx, id, uc.owner, uc.now().Add(uc.lease))
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

// step takes one step of r and reports whether to take another.
func (uc *UseCase) step(ctx context.Context, r model.Rollout) bool {
	if r.StopRequested {
		uc.finish(ctx, r, model.RolloutStopped, "", "", "")
		return false
	}
	k, ok := uc.kinds[r.Kind]
	if !ok {
		uc.finish(ctx, r, model.RolloutFailed, "unknown rollout kind "+r.Kind, "", "")
		return false
	}
	p, err := k.next(ctx, r)
	switch {
	case errors.Is(err, errBadScope):
		uc.finish(ctx, r, model.RolloutFailed, err.Error(), "", "")
		return false
	case err != nil:
		// A database error: the lease expires and a pass retries.
		uc.d.Log.Warn("rollouts: pick the next app", "rollout_id", r.ID, "error", err)
		return false
	}
	if p.appID == "" {
		if p.wait {
			return sleep(ctx, uc.settlePoll)
		}
		ok, err := uc.d.Repo.Complete(ctx, r.ID, uc.owner, r.ScopeRev)
		if err != nil {
			uc.d.Log.Warn("rollouts: complete", "rollout_id", r.ID, "error", err)
			return false
		}
		if ok {
			uc.ended(ctx, r.ID)
			return false
		}
		return true // the scope was extended meanwhile: look again
	}
	total := r.Done + r.Skipped + p.remaining
	if ok, err := uc.d.Repo.StartStep(ctx, r.ID, uc.owner, p.appID, p.serverID, total); err != nil || !ok {
		return false
	}
	uc.changed(ctx, r.ID)
	res := k.run(ctx, r, p)
	if ctx.Err() != nil {
		return false // the lease is gone or the process stops: the step is redone
	}
	switch res.outcome {
	case stepFailed:
		uc.finish(ctx, r, model.RolloutFailed, res.err, p.appID, p.serverID)
		return false
	case stepSkipped:
		ok, err = uc.d.Repo.CountStep(ctx, r.ID, uc.owner, 0, 1, append(slices.Clone(r.SkipIDs), p.skipID))
	default:
		ok, err = uc.d.Repo.CountStep(ctx, r.ID, uc.owner, 1, 0, r.SkipIDs)
	}
	if err != nil || !ok {
		return false
	}
	uc.changed(ctx, r.ID)
	return true
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
	r := uc.changed(ctx, id)
	if r.ID == "" {
		return
	}
	if err := uc.d.Repo.Prune(ctx, r.Kind, r.ScopeKey, keepEnded); err != nil {
		uc.d.Log.Warn("rollouts: prune", "rollout_id", id, "error", err)
	}
}

// changed announces the rollout's new counts or state to its org.
func (uc *UseCase) changed(ctx context.Context, id string) model.Rollout {
	r, err := uc.d.Repo.GetRollout(ctx, id)
	if err != nil {
		uc.d.Log.Warn("rollouts: read for an event", "rollout_id", id, "error", err)
		return model.Rollout{}
	}
	uc.publish(ctx, r.OrgID, uc.view(ctx, r))
	return r
}

// sleep waits d unless ctx ends first; it reports whether ctx is still
// live.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
