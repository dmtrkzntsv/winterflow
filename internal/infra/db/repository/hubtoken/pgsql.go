package hubtoken

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) InsertHubJoinToken(ctx context.Context, db backend.DBTX, p insertParams) error {
	return pgsqlgen.New(db).InsertHubJoinToken(ctx, pgsqlgen.InsertHubJoinTokenParams(p))
}

func (pgsqlQuerier) ConsumeHubJoinToken(ctx context.Context, db backend.DBTX, p consumeParams) (int64, error) {
	return pgsqlgen.New(db).ConsumeHubJoinToken(ctx, pgsqlgen.ConsumeHubJoinTokenParams(p))
}

func (pgsqlQuerier) PruneHubJoinTokens(ctx context.Context, db backend.DBTX, p pruneParams) error {
	return pgsqlgen.New(db).PruneHubJoinTokens(ctx, pgsqlgen.PruneHubJoinTokensParams(p))
}
