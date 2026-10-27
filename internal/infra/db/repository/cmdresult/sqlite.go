package cmdresult

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) InsertCommandResult(ctx context.Context, db backend.DBTX, p insertParams) error {
	return sqlitegen.New(db).InsertCommandResult(ctx, p)
}

func (sqliteQuerier) FinishCommandResult(ctx context.Context, db backend.DBTX, p finishParams) (int64, error) {
	return sqlitegen.New(db).FinishCommandResult(ctx, p)
}

func (sqliteQuerier) GetCommandResult(ctx context.Context, db backend.DBTX, p getParams) (row, error) {
	return sqlitegen.New(db).GetCommandResult(ctx, p)
}

func (sqliteQuerier) MarkStaleCommandResults(ctx context.Context, db backend.DBTX, p staleParams) (int64, error) {
	return sqlitegen.New(db).MarkStaleCommandResults(ctx, p)
}

func (sqliteQuerier) PruneCommandResults(ctx context.Context, db backend.DBTX, cutoff *types.DateTime) (int64, error) {
	return sqlitegen.New(db).PruneCommandResults(ctx, cutoff)
}
