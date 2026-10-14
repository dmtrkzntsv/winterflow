package rollout

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
	"winterflow/internal/infra/db/types"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) InsertRollout(ctx context.Context, db backend.DBTX, p insertParams) (int64, error) {
	return pgsqlgen.New(db).InsertRollout(ctx, pgsqlgen.InsertRolloutParams(p))
}

func (pgsqlQuerier) GetRollout(ctx context.Context, db backend.DBTX, id string) (rolloutRow, error) {
	row, err := pgsqlgen.New(db).GetRollout(ctx, id)
	return rolloutRow(row), err
}

func (pgsqlQuerier) GetActiveRollout(ctx context.Context, db backend.DBTX, p activeParams) (rolloutRow, error) {
	row, err := pgsqlgen.New(db).GetActiveRollout(ctx, pgsqlgen.GetActiveRolloutParams(p))
	return rolloutRow(row), err
}

func (pgsqlQuerier) ListRolloutsForScope(ctx context.Context, db backend.DBTX, p scopeParams) ([]rolloutRow, error) {
	rows, err := pgsqlgen.New(db).ListRolloutsForScope(ctx, pgsqlgen.ListRolloutsForScopeParams(p))
	if err != nil {
		return nil, err
	}
	out := make([]rolloutRow, len(rows))
	for i, row := range rows {
		out[i] = rolloutRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListRolloutsOfKind(ctx context.Context, db backend.DBTX, p kindParams) ([]rolloutRow, error) {
	rows, err := pgsqlgen.New(db).ListRolloutsOfKind(ctx, pgsqlgen.ListRolloutsOfKindParams(p))
	if err != nil {
		return nil, err
	}
	out := make([]rolloutRow, len(rows))
	for i, row := range rows {
		out[i] = rolloutRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ExtendRollout(ctx context.Context, db backend.DBTX, p extendParams) (int64, error) {
	return pgsqlgen.New(db).ExtendRollout(ctx, pgsqlgen.ExtendRolloutParams(p))
}

func (pgsqlQuerier) RequestRolloutStop(ctx context.Context, db backend.DBTX, p stopParams) (int64, error) {
	return pgsqlgen.New(db).RequestRolloutStop(ctx, pgsqlgen.RequestRolloutStopParams(p))
}

func (pgsqlQuerier) ListClaimableRollouts(ctx context.Context, db backend.DBTX, now *types.DateTime) ([]string, error) {
	return pgsqlgen.New(db).ListClaimableRollouts(ctx, now)
}

func (pgsqlQuerier) ClaimRollout(ctx context.Context, db backend.DBTX, p claimParams) (int64, error) {
	return pgsqlgen.New(db).ClaimRollout(ctx, pgsqlgen.ClaimRolloutParams(p))
}

func (pgsqlQuerier) RenewRolloutLease(ctx context.Context, db backend.DBTX, p renewParams) (int64, error) {
	return pgsqlgen.New(db).RenewRolloutLease(ctx, pgsqlgen.RenewRolloutLeaseParams(p))
}

func (pgsqlQuerier) ReleaseRolloutLease(ctx context.Context, db backend.DBTX, p releaseParams) (int64, error) {
	return pgsqlgen.New(db).ReleaseRolloutLease(ctx, pgsqlgen.ReleaseRolloutLeaseParams(p))
}

func (pgsqlQuerier) StopUnleasedRollout(ctx context.Context, db backend.DBTX, p unleasedParam) (int64, error) {
	return pgsqlgen.New(db).StopUnleasedRollout(ctx, pgsqlgen.StopUnleasedRolloutParams(p))
}

func (pgsqlQuerier) StartRolloutStep(ctx context.Context, db backend.DBTX, p startParams) (int64, error) {
	return pgsqlgen.New(db).StartRolloutStep(ctx, pgsqlgen.StartRolloutStepParams(p))
}

func (pgsqlQuerier) CountRolloutStep(ctx context.Context, db backend.DBTX, p countParams) (int64, error) {
	return pgsqlgen.New(db).CountRolloutStep(ctx, pgsqlgen.CountRolloutStepParams(p))
}

func (pgsqlQuerier) FinishRollout(ctx context.Context, db backend.DBTX, p finishParams) (int64, error) {
	return pgsqlgen.New(db).FinishRollout(ctx, pgsqlgen.FinishRolloutParams(p))
}

func (pgsqlQuerier) CompleteRollout(ctx context.Context, db backend.DBTX, p completeParam) (int64, error) {
	return pgsqlgen.New(db).CompleteRollout(ctx, pgsqlgen.CompleteRolloutParams(p))
}

func (pgsqlQuerier) PruneRollouts(ctx context.Context, db backend.DBTX, p pruneParams) error {
	return pgsqlgen.New(db).PruneRollouts(ctx, pgsqlgen.PruneRolloutsParams(p))
}
