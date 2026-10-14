package port

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
)

// RolloutRepository persists the rollout queue. Every worker write names
// the lease owner: a worker whose lease was taken over changes nothing
// (the call reports false).
type RolloutRepository interface {
	// InsertRollout inserts r as queued; false when its (kind, scope_key)
	// already has an active rollout.
	InsertRollout(ctx context.Context, r model.Rollout) (bool, error)
	// GetRollout returns a rollout; model.ErrRolloutNotFound when unknown.
	GetRollout(ctx context.Context, id string) (model.Rollout, error)
	// GetActiveRollout returns the queued or running rollout of a scope;
	// model.ErrRolloutNotFound when there is none.
	GetActiveRollout(ctx context.Context, kind, scopeKey string) (model.Rollout, error)
	// ListRollouts returns an org's rollouts of kind (of one scope unless
	// scopeKey is ""): the active ones first, then the ended ones newest
	// first.
	ListRollouts(ctx context.Context, orgID, kind, scopeKey string) ([]model.Rollout, error)
	// ExtendRollout replaces an active rollout's scope if its scope_rev is
	// still rev, bumping it; false when the scope moved on, it ended or a stop
	// was requested.
	ExtendRollout(ctx context.Context, id string, rev int64, scope []byte) (bool, error)
	// RequestStop asks an active rollout of the org to stop; false when it
	// is not active (or not the org's).
	RequestStop(ctx context.Context, orgID, id string) (bool, error)

	// ListClaimable returns active rollouts without a live lease at now.
	ListClaimable(ctx context.Context, now time.Time) ([]string, error)
	// Claim takes the lease (until) and marks the rollout running, if it
	// is active and nobody holds a live lease at now.
	Claim(ctx context.Context, id, owner string, now, until time.Time) (bool, error)
	RenewLease(ctx context.Context, id, owner string, until time.Time) (bool, error)
	// ReleaseLease gives the owner's lease back; the rollout stays active.
	// A zero until makes it claimable at once (a graceful shutdown), a time
	// parks it until then (a rollout waiting for a server).
	ReleaseLease(ctx context.Context, id, owner string, until time.Time) (bool, error)
	// StopUnleased ends the org's active rollout as stopped if nobody holds a
	// live lease on it at now (queued, parked, or its owner's lease expired);
	// false otherwise (RequestStop then asks the owner).
	StopUnleased(ctx context.Context, orgID, id string, now time.Time) (bool, error)
	// StartStep records the app a step works on and the total as of now.
	StartStep(ctx context.Context, id, owner, appID, serverID string, total int64) (bool, error)
	// CountStep adds a step's outcome (skipIDs replaces the skip list).
	CountStep(ctx context.Context, id, owner string, done, skipped int64, skipIDs []string) (bool, error)
	// Finish ends the rollout stopped or failed, naming the app (a failure
	// names the one that failed) and releasing the lease.
	Finish(ctx context.Context, id, owner string, state model.RolloutState, errMsg, appID, serverID string) (bool, error)
	// Complete ends the rollout done, unless its scope_rev moved past rev.
	Complete(ctx context.Context, id, owner string, rev int64) (bool, error)
	// Prune deletes the ended rollouts of a scope beyond the newest keep.
	Prune(ctx context.Context, kind, scopeKey string, keep int) error
}
