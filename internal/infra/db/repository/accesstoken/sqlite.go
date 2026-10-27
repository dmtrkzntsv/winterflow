package accesstoken

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) InsertAccessToken(ctx context.Context, db backend.DBTX, p insertParams) error {
	return sqlitegen.New(db).InsertAccessToken(ctx, p)
}

func (sqliteQuerier) GetAccessTokenByHash(ctx context.Context, db backend.DBTX, hash string) (tokenRow, error) {
	return sqlitegen.New(db).GetAccessTokenByHash(ctx, hash)
}

func (sqliteQuerier) TouchAccessToken(ctx context.Context, db backend.DBTX, p touchParams) (int64, error) {
	return sqlitegen.New(db).TouchAccessToken(ctx, p)
}

func (sqliteQuerier) RevokeAccessToken(ctx context.Context, db backend.DBTX, p revokeParams) error {
	return sqlitegen.New(db).RevokeAccessToken(ctx, p)
}

func (sqliteQuerier) RevokeUserAccessToken(ctx context.Context, db backend.DBTX, p revokeUserParams) (int64, error) {
	return sqlitegen.New(db).RevokeUserAccessToken(ctx, p)
}

func (sqliteQuerier) RevokeUserAccessTokens(ctx context.Context, db backend.DBTX, p revokeKindParams) error {
	return sqlitegen.New(db).RevokeUserAccessTokens(ctx, p)
}

func (sqliteQuerier) RevokeAccessTokensByGrant(ctx context.Context, db backend.DBTX, p revokeGrantParams) error {
	return sqlitegen.New(db).RevokeAccessTokensByGrant(ctx, p)
}

func (sqliteQuerier) ListAccessTokensByUser(ctx context.Context, db backend.DBTX, p listParams) ([]tokenRow, error) {
	return sqlitegen.New(db).ListAccessTokensByUser(ctx, p)
}

func (sqliteQuerier) DeleteDeadAccessTokens(ctx context.Context, db backend.DBTX, p deleteDeadParams) (int64, error) {
	return sqlitegen.New(db).DeleteDeadAccessTokens(ctx, p)
}
