package accesstoken

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) InsertAccessToken(ctx context.Context, db backend.DBTX, p insertParams) error {
	return pgsqlgen.New(db).InsertAccessToken(ctx, pgsqlgen.InsertAccessTokenParams(p))
}

func (pgsqlQuerier) GetAccessTokenByHash(ctx context.Context, db backend.DBTX, hash string) (tokenRow, error) {
	row, err := pgsqlgen.New(db).GetAccessTokenByHash(ctx, hash)
	return tokenRow(row), err
}

func (pgsqlQuerier) TouchAccessToken(ctx context.Context, db backend.DBTX, p touchParams) (int64, error) {
	return pgsqlgen.New(db).TouchAccessToken(ctx, pgsqlgen.TouchAccessTokenParams(p))
}

func (pgsqlQuerier) RevokeAccessToken(ctx context.Context, db backend.DBTX, p revokeParams) error {
	return pgsqlgen.New(db).RevokeAccessToken(ctx, pgsqlgen.RevokeAccessTokenParams(p))
}

func (pgsqlQuerier) RevokeUserAccessToken(ctx context.Context, db backend.DBTX, p revokeUserParams) (int64, error) {
	return pgsqlgen.New(db).RevokeUserAccessToken(ctx, pgsqlgen.RevokeUserAccessTokenParams(p))
}

func (pgsqlQuerier) RevokeUserAccessTokens(ctx context.Context, db backend.DBTX, p revokeKindParams) error {
	return pgsqlgen.New(db).RevokeUserAccessTokens(ctx, pgsqlgen.RevokeUserAccessTokensParams(p))
}

func (pgsqlQuerier) RevokeAccessTokensByGrant(ctx context.Context, db backend.DBTX, p revokeGrantParams) error {
	return pgsqlgen.New(db).RevokeAccessTokensByGrant(ctx, pgsqlgen.RevokeAccessTokensByGrantParams(p))
}

func (pgsqlQuerier) ListAccessTokensByUser(ctx context.Context, db backend.DBTX, p listParams) ([]tokenRow, error) {
	rows, err := pgsqlgen.New(db).ListAccessTokensByUser(ctx, pgsqlgen.ListAccessTokensByUserParams(p))
	if err != nil {
		return nil, err
	}
	out := make([]tokenRow, len(rows))
	for i, row := range rows {
		out[i] = tokenRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) DeleteDeadAccessTokens(ctx context.Context, db backend.DBTX, p deleteDeadParams) (int64, error) {
	return pgsqlgen.New(db).DeleteDeadAccessTokens(ctx, pgsqlgen.DeleteDeadAccessTokensParams(p))
}
