package secretreq

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) InsertSecretRequest(ctx context.Context, db backend.DBTX, p insertParams) error {
	return sqlitegen.New(db).InsertSecretRequest(ctx, p)
}

func (sqliteQuerier) GetSecretRequestByTokenHash(ctx context.Context, db backend.DBTX, tokenHash string) (row, error) {
	return sqlitegen.New(db).GetSecretRequestByTokenHash(ctx, tokenHash)
}

func (sqliteQuerier) GetSecretRequest(ctx context.Context, db backend.DBTX, p getParams) (row, error) {
	return sqlitegen.New(db).GetSecretRequest(ctx, p)
}

func (sqliteQuerier) SubmitSecretRequest(ctx context.Context, db backend.DBTX, p submitParams) (int64, error) {
	return sqlitegen.New(db).SubmitSecretRequest(ctx, p)
}

func (sqliteQuerier) UseSecretRequest(ctx context.Context, db backend.DBTX, p useParams) (int64, error) {
	return sqlitegen.New(db).UseSecretRequest(ctx, p)
}

func (sqliteQuerier) ClaimSecretRequest(ctx context.Context, db backend.DBTX, p claimParams) (int64, error) {
	return sqlitegen.New(db).ClaimSecretRequest(ctx, p)
}

func (sqliteQuerier) HoldSecretRequest(ctx context.Context, db backend.DBTX, p holdParams) (int64, error) {
	return sqlitegen.New(db).HoldSecretRequest(ctx, p)
}

func (sqliteQuerier) ReleaseSecretRequest(ctx context.Context, db backend.DBTX, p releaseParams) (int64, error) {
	return sqlitegen.New(db).ReleaseSecretRequest(ctx, p)
}

func (sqliteQuerier) ExpireSecretRequests(ctx context.Context, db backend.DBTX, now *types.DateTime) (int64, error) {
	return sqlitegen.New(db).ExpireSecretRequests(ctx, now)
}

func (sqliteQuerier) PruneSecretRequests(ctx context.Context, db backend.DBTX, cutoff *types.DateTime) (int64, error) {
	return sqlitegen.New(db).PruneSecretRequests(ctx, cutoff)
}
