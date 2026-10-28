package cmdresult

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
	"winterflow/internal/infra/db/types"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) InsertCommandResult(ctx context.Context, db backend.DBTX, p insertParams) error {
	return pgsqlgen.New(db).InsertCommandResult(ctx, pgsqlgen.InsertCommandResultParams(p))
}

func (pgsqlQuerier) FinishCommandResult(ctx context.Context, db backend.DBTX, p finishParams) (int64, error) {
	return pgsqlgen.New(db).FinishCommandResult(ctx, pgsqlgen.FinishCommandResultParams(p))
}

func (pgsqlQuerier) GetCommandResult(ctx context.Context, db backend.DBTX, p getParams) (row, error) {
	got, err := pgsqlgen.New(db).GetCommandResult(ctx, pgsqlgen.GetCommandResultParams(p))
	return row(got), err
}

func (pgsqlQuerier) MarkStaleCommandResults(ctx context.Context, db backend.DBTX, p staleParams) (int64, error) {
	return pgsqlgen.New(db).MarkStaleCommandResults(ctx, pgsqlgen.MarkStaleCommandResultsParams(p))
}

func (pgsqlQuerier) PruneCommandResults(ctx context.Context, db backend.DBTX, cutoff *types.DateTime) (int64, error) {
	return pgsqlgen.New(db).PruneCommandResults(ctx, cutoff)
}
