// Package rollout implements port.RolloutRepository over the rollouts
// table.
package rollout

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

// Row and parameter types are SQLite's; pgsql.go converts Postgres's
// identically shaped ones.
type (
	rolloutRow    = sqlitegen.Rollout
	insertParams  = sqlitegen.InsertRolloutParams
	activeParams  = sqlitegen.GetActiveRolloutParams
	scopeParams   = sqlitegen.ListRolloutsForScopeParams
	kindParams    = sqlitegen.ListRolloutsOfKindParams
	extendParams  = sqlitegen.ExtendRolloutParams
	stopParams    = sqlitegen.RequestRolloutStopParams
	claimParams   = sqlitegen.ClaimRolloutParams
	renewParams   = sqlitegen.RenewRolloutLeaseParams
	startParams   = sqlitegen.StartRolloutStepParams
	countParams   = sqlitegen.CountRolloutStepParams
	finishParams  = sqlitegen.FinishRolloutParams
	completeParam = sqlitegen.CompleteRolloutParams
	pruneParams   = sqlitegen.PruneRolloutsParams
)

type querier interface {
	InsertRollout(ctx context.Context, db backend.DBTX, p insertParams) (int64, error)
	GetRollout(ctx context.Context, db backend.DBTX, id string) (rolloutRow, error)
	GetActiveRollout(ctx context.Context, db backend.DBTX, p activeParams) (rolloutRow, error)
	ListRolloutsForScope(ctx context.Context, db backend.DBTX, p scopeParams) ([]rolloutRow, error)
	ListRolloutsOfKind(ctx context.Context, db backend.DBTX, p kindParams) ([]rolloutRow, error)
	ExtendRollout(ctx context.Context, db backend.DBTX, p extendParams) (int64, error)
	RequestRolloutStop(ctx context.Context, db backend.DBTX, p stopParams) (int64, error)
	ListClaimableRollouts(ctx context.Context, db backend.DBTX, now *types.DateTime) ([]string, error)
	ClaimRollout(ctx context.Context, db backend.DBTX, p claimParams) (int64, error)
	RenewRolloutLease(ctx context.Context, db backend.DBTX, p renewParams) (int64, error)
	StartRolloutStep(ctx context.Context, db backend.DBTX, p startParams) (int64, error)
	CountRolloutStep(ctx context.Context, db backend.DBTX, p countParams) (int64, error)
	FinishRollout(ctx context.Context, db backend.DBTX, p finishParams) (int64, error)
	CompleteRollout(ctx context.Context, db backend.DBTX, p completeParam) (int64, error)
	PruneRollouts(ctx context.Context, db backend.DBTX, p pruneParams) error
}

// Repo is the rollouts repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.RolloutRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("rollout repository: unknown database driver %q", driver)
}

func at(t time.Time) *types.DateTime {
	dt := types.DateTime(t)
	return &dt
}

func affected(n int64, err error) (bool, error) { return n > 0, err }

// InsertRollout inserts r as queued unless its scope has an active one.
func (r *Repo) InsertRollout(ctx context.Context, ro model.Rollout) (bool, error) {
	return affected(r.q.InsertRollout(ctx, r.tx.Querier(ctx), insertParams{
		ID: ro.ID, OrgID: ro.OrgID, Kind: ro.Kind, Scope: string(ro.Scope), ScopeKey: ro.ScopeKey,
		CreatedAt: types.NewDateTime(), CreatedBy: ro.CreatedBy,
	}))
}

// GetRollout returns a rollout by id.
func (r *Repo) GetRollout(ctx context.Context, id string) (model.Rollout, error) {
	return one(r.q.GetRollout(ctx, r.tx.Querier(ctx), id))
}

// GetActiveRollout returns the queued or running rollout of a scope.
func (r *Repo) GetActiveRollout(ctx context.Context, kind, scopeKey string) (model.Rollout, error) {
	return one(r.q.GetActiveRollout(ctx, r.tx.Querier(ctx), activeParams{Kind: kind, ScopeKey: scopeKey}))
}

// ListRollouts returns an org's rollouts of kind, active first.
func (r *Repo) ListRollouts(ctx context.Context, orgID, kind, scopeKey string) ([]model.Rollout, error) {
	var (
		rows []rolloutRow
		err  error
	)
	if scopeKey == "" {
		rows, err = r.q.ListRolloutsOfKind(ctx, r.tx.Querier(ctx), kindParams{OrgID: orgID, Kind: kind})
	} else {
		rows, err = r.q.ListRolloutsForScope(ctx, r.tx.Querier(ctx), scopeParams{OrgID: orgID, Kind: kind, ScopeKey: scopeKey})
	}
	if err != nil {
		return nil, err
	}
	out := make([]model.Rollout, 0, len(rows))
	for _, row := range rows {
		ro, err := hydrate(row)
		if err != nil {
			return nil, err
		}
		out = append(out, ro)
	}
	return out, nil
}

// ExtendRollout replaces the scope while scope_rev is still rev.
func (r *Repo) ExtendRollout(ctx context.Context, id string, rev int64, scope []byte) (bool, error) {
	return affected(r.q.ExtendRollout(ctx, r.tx.Querier(ctx), extendParams{Scope: string(scope), ID: id, ScopeRev: rev}))
}

// RequestStop sets stop_requested on an active rollout of the org.
func (r *Repo) RequestStop(ctx context.Context, orgID, id string) (bool, error) {
	return affected(r.q.RequestRolloutStop(ctx, r.tx.Querier(ctx), stopParams{ID: id, OrgID: orgID}))
}

// ListClaimable returns active rollouts without a live lease, oldest first.
func (r *Repo) ListClaimable(ctx context.Context, now time.Time) ([]string, error) {
	return r.q.ListClaimableRollouts(ctx, r.tx.Querier(ctx), at(now))
}

// Claim takes the lease of an active rollout nobody holds.
func (r *Repo) Claim(ctx context.Context, id, owner string, now, until time.Time) (bool, error) {
	return affected(r.q.ClaimRollout(ctx, r.tx.Querier(ctx), claimParams{
		LeaseOwner: owner, LeaseUntil: at(until), ID: id, LeaseUntil_2: at(now),
	}))
}

// RenewLease moves the owner's lease to until.
func (r *Repo) RenewLease(ctx context.Context, id, owner string, until time.Time) (bool, error) {
	return affected(r.q.RenewRolloutLease(ctx, r.tx.Querier(ctx), renewParams{LeaseUntil: at(until), ID: id, LeaseOwner: owner}))
}

// StartStep records the app in flight and the total.
func (r *Repo) StartStep(ctx context.Context, id, owner, appID, serverID string, total int64) (bool, error) {
	return affected(r.q.StartRolloutStep(ctx, r.tx.Querier(ctx), startParams{
		CurrentAppID: appID, CurrentServerID: serverID, Total: total, ID: id, LeaseOwner: owner,
	}))
}

// CountStep adds a step's outcome.
func (r *Repo) CountStep(ctx context.Context, id, owner string, done, skipped int64, skipIDs []string) (bool, error) {
	if skipIDs == nil {
		skipIDs = []string{}
	}
	raw, err := json.Marshal(skipIDs)
	if err != nil {
		return false, err
	}
	return affected(r.q.CountRolloutStep(ctx, r.tx.Querier(ctx), countParams{
		Done: done, Skipped: skipped, SkipIds: string(raw), ID: id, LeaseOwner: owner,
	}))
}

// Finish ends the rollout stopped or failed.
func (r *Repo) Finish(ctx context.Context, id, owner string, state model.RolloutState, errMsg, appID, serverID string) (bool, error) {
	return affected(r.q.FinishRollout(ctx, r.tx.Querier(ctx), finishParams{
		State: string(state), Error: errMsg, CurrentAppID: appID, CurrentServerID: serverID,
		FinishedAt: at(time.Now()), ID: id, LeaseOwner: owner,
	}))
}

// Complete ends the rollout done while scope_rev is still rev.
func (r *Repo) Complete(ctx context.Context, id, owner string, rev int64) (bool, error) {
	return affected(r.q.CompleteRollout(ctx, r.tx.Querier(ctx), completeParam{
		FinishedAt: at(time.Now()), ID: id, LeaseOwner: owner, ScopeRev: rev,
	}))
}

// Prune keeps the newest keep ended rollouts of a scope.
func (r *Repo) Prune(ctx context.Context, kind, scopeKey string, keep int) error {
	return r.q.PruneRollouts(ctx, r.tx.Querier(ctx), pruneParams{Kind: kind, ScopeKey: scopeKey, Keep: int64(keep)})
}

func one(row rolloutRow, err error) (model.Rollout, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return model.Rollout{}, model.ErrRolloutNotFound
	}
	if err != nil {
		return model.Rollout{}, err
	}
	return hydrate(row)
}

func hydrate(row rolloutRow) (model.Rollout, error) {
	out := model.Rollout{
		ID: row.ID, OrgID: row.OrgID, Kind: row.Kind, Scope: []byte(row.Scope), ScopeKey: row.ScopeKey,
		ScopeRev: row.ScopeRev, State: model.RolloutState(row.State), StopRequested: row.StopRequested,
		CurrentAppID: row.CurrentAppID, CurrentServerID: row.CurrentServerID, Done: row.Done,
		Skipped: row.Skipped, Total: row.Total, Error: row.Error, LeaseOwner: row.LeaseOwner,
		CreatedAt: row.CreatedAt.Time(), CreatedBy: row.CreatedBy,
		LeaseUntil: timePtr(row.LeaseUntil), FinishedAt: timePtr(row.FinishedAt),
	}
	if err := json.Unmarshal([]byte(row.SkipIds), &out.SkipIDs); err != nil {
		return model.Rollout{}, fmt.Errorf("rollout %s: skip_ids: %w", row.ID, err)
	}
	return out, nil
}

func timePtr(dt *types.DateTime) *time.Time {
	if dt == nil {
		return nil
	}
	t := dt.Time()
	return &t
}
