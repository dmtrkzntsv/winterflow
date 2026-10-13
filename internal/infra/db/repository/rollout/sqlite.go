package rollout

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) InsertRollout(ctx context.Context, db backend.DBTX, p insertParams) (int64, error) {
	return sqlitegen.New(db).InsertRollout(ctx, p)
}

func (sqliteQuerier) GetRollout(ctx context.Context, db backend.DBTX, id string) (rolloutRow, error) {
	return sqlitegen.New(db).GetRollout(ctx, id)
}

func (sqliteQuerier) GetActiveRollout(ctx context.Context, db backend.DBTX, p activeParams) (rolloutRow, error) {
	return sqlitegen.New(db).GetActiveRollout(ctx, p)
}

func (sqliteQuerier) ListRolloutsForScope(ctx context.Context, db backend.DBTX, p scopeParams) ([]rolloutRow, error) {
	return sqlitegen.New(db).ListRolloutsForScope(ctx, p)
}

func (sqliteQuerier) ListRolloutsOfKind(ctx context.Context, db backend.DBTX, p kindParams) ([]rolloutRow, error) {
	return sqlitegen.New(db).ListRolloutsOfKind(ctx, p)
}

func (sqliteQuerier) ExtendRollout(ctx context.Context, db backend.DBTX, p extendParams) (int64, error) {
	return sqlitegen.New(db).ExtendRollout(ctx, p)
}

func (sqliteQuerier) RequestRolloutStop(ctx context.Context, db backend.DBTX, p stopParams) (int64, error) {
	return sqlitegen.New(db).RequestRolloutStop(ctx, p)
}

func (sqliteQuerier) ListClaimableRollouts(ctx context.Context, db backend.DBTX, now *types.DateTime) ([]string, error) {
	return sqlitegen.New(db).ListClaimableRollouts(ctx, now)
}

func (sqliteQuerier) ClaimRollout(ctx context.Context, db backend.DBTX, p claimParams) (int64, error) {
	return sqlitegen.New(db).ClaimRollout(ctx, p)
}

func (sqliteQuerier) RenewRolloutLease(ctx context.Context, db backend.DBTX, p renewParams) (int64, error) {
	return sqlitegen.New(db).RenewRolloutLease(ctx, p)
}

func (sqliteQuerier) ReleaseRolloutLease(ctx context.Context, db backend.DBTX, p releaseParams) (int64, error) {
	return sqlitegen.New(db).ReleaseRolloutLease(ctx, p)
}

func (sqliteQuerier) StartRolloutStep(ctx context.Context, db backend.DBTX, p startParams) (int64, error) {
	return sqlitegen.New(db).StartRolloutStep(ctx, p)
}

func (sqliteQuerier) CountRolloutStep(ctx context.Context, db backend.DBTX, p countParams) (int64, error) {
	return sqlitegen.New(db).CountRolloutStep(ctx, p)
}

func (sqliteQuerier) FinishRollout(ctx context.Context, db backend.DBTX, p finishParams) (int64, error) {
	return sqlitegen.New(db).FinishRollout(ctx, p)
}

func (sqliteQuerier) CompleteRollout(ctx context.Context, db backend.DBTX, p completeParam) (int64, error) {
	return sqlitegen.New(db).CompleteRollout(ctx, p)
}

func (sqliteQuerier) PruneRollouts(ctx context.Context, db backend.DBTX, p pruneParams) error {
	return sqlitegen.New(db).PruneRollouts(ctx, p)
}
