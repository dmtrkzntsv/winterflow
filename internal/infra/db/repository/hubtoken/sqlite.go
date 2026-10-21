package hubtoken

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) InsertHubJoinToken(ctx context.Context, db backend.DBTX, p insertParams) error {
	return sqlitegen.New(db).InsertHubJoinToken(ctx, p)
}

func (sqliteQuerier) ConsumeHubJoinToken(ctx context.Context, db backend.DBTX, p consumeParams) (int64, error) {
	return sqlitegen.New(db).ConsumeHubJoinToken(ctx, p)
}

func (sqliteQuerier) PruneHubJoinTokens(ctx context.Context, db backend.DBTX, p pruneParams) error {
	return sqlitegen.New(db).PruneHubJoinTokens(ctx, p)
}
