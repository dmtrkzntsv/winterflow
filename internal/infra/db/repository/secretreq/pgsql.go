package secretreq

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
	"winterflow/internal/infra/db/types"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) InsertSecretRequest(ctx context.Context, db backend.DBTX, p insertParams) error {
	return pgsqlgen.New(db).InsertSecretRequest(ctx, pgsqlgen.InsertSecretRequestParams(p))
}

func (pgsqlQuerier) GetSecretRequestByTokenHash(ctx context.Context, db backend.DBTX, tokenHash string) (row, error) {
	got, err := pgsqlgen.New(db).GetSecretRequestByTokenHash(ctx, tokenHash)
	return row(got), err
}

func (pgsqlQuerier) GetSecretRequest(ctx context.Context, db backend.DBTX, p getParams) (row, error) {
	got, err := pgsqlgen.New(db).GetSecretRequest(ctx, pgsqlgen.GetSecretRequestParams(p))
	return row(got), err
}

func (pgsqlQuerier) SubmitSecretRequest(ctx context.Context, db backend.DBTX, p submitParams) (int64, error) {
	return pgsqlgen.New(db).SubmitSecretRequest(ctx, pgsqlgen.SubmitSecretRequestParams(p))
}

func (pgsqlQuerier) UseSecretRequest(ctx context.Context, db backend.DBTX, p useParams) (int64, error) {
	return pgsqlgen.New(db).UseSecretRequest(ctx, pgsqlgen.UseSecretRequestParams(p))
}

func (pgsqlQuerier) ExpireSecretRequests(ctx context.Context, db backend.DBTX, p expireParams) (int64, error) {
	return pgsqlgen.New(db).ExpireSecretRequests(ctx, pgsqlgen.ExpireSecretRequestsParams(p))
}

func (pgsqlQuerier) PruneSecretRequests(ctx context.Context, db backend.DBTX, cutoff *types.DateTime) (int64, error) {
	return pgsqlgen.New(db).PruneSecretRequests(ctx, cutoff)
}
