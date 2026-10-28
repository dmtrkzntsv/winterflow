package authserver

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) InsertOAuthClient(ctx context.Context, db backend.DBTX, p insertClientParam) error {
	return sqlitegen.New(db).InsertOAuthClient(ctx, p)
}

func (sqliteQuerier) GetOAuthClient(ctx context.Context, db backend.DBTX, id string) (clientRow, error) {
	return sqlitegen.New(db).GetOAuthClient(ctx, id)
}

func (sqliteQuerier) MarkOAuthClientUsed(ctx context.Context, db backend.DBTX, p markClientParams) error {
	return sqlitegen.New(db).MarkOAuthClientUsed(ctx, p)
}

func (sqliteQuerier) PurgeUnusedOAuthClients(ctx context.Context, db backend.DBTX, createdBefore types.DateTime) (int64, error) {
	return sqlitegen.New(db).PurgeUnusedOAuthClients(ctx, createdBefore)
}

func (sqliteQuerier) CountUnusedOAuthClients(ctx context.Context, db backend.DBTX) (int64, error) {
	return sqlitegen.New(db).CountUnusedOAuthClients(ctx)
}

func (sqliteQuerier) InsertOAuthCode(ctx context.Context, db backend.DBTX, p insertCodeParams) error {
	return sqlitegen.New(db).InsertOAuthCode(ctx, p)
}

func (sqliteQuerier) ConsumeOAuthCode(ctx context.Context, db backend.DBTX, codeHash string) (codeRow, error) {
	return sqlitegen.New(db).ConsumeOAuthCode(ctx, codeHash)
}

func (sqliteQuerier) PurgeExpiredOAuthCodes(ctx context.Context, db backend.DBTX, before types.DateTime) error {
	return sqlitegen.New(db).PurgeExpiredOAuthCodes(ctx, before)
}

func (sqliteQuerier) InsertOAuthGrant(ctx context.Context, db backend.DBTX, p insertGrantParams) error {
	return sqlitegen.New(db).InsertOAuthGrant(ctx, p)
}

func (sqliteQuerier) GetOAuthGrant(ctx context.Context, db backend.DBTX, id string) (grantRow, error) {
	return sqlitegen.New(db).GetOAuthGrant(ctx, id)
}

func (sqliteQuerier) GetOAuthGrantByRefreshHash(ctx context.Context, db backend.DBTX, hash string) (grantRow, error) {
	return sqlitegen.New(db).GetOAuthGrantByRefreshHash(ctx, hash)
}

func (sqliteQuerier) GetOAuthGrantBySpentRefreshHash(ctx context.Context, db backend.DBTX, hash string) (grantRow, error) {
	return sqlitegen.New(db).GetOAuthGrantBySpentRefreshHash(ctx, hash)
}

func (sqliteQuerier) InsertOAuthSpentRefreshHash(ctx context.Context, db backend.DBTX, p insertSpentParams) error {
	return sqlitegen.New(db).InsertOAuthSpentRefreshHash(ctx, p)
}

func (sqliteQuerier) RotateOAuthGrant(ctx context.Context, db backend.DBTX, p rotateParams) (int64, error) {
	return sqlitegen.New(db).RotateOAuthGrant(ctx, p)
}

func (sqliteQuerier) RevokeOAuthGrant(ctx context.Context, db backend.DBTX, p revokeParams) (int64, error) {
	return sqlitegen.New(db).RevokeOAuthGrant(ctx, p)
}

func (sqliteQuerier) RevokeUserOAuthGrants(ctx context.Context, db backend.DBTX, p revokeUserParams) (int64, error) {
	return sqlitegen.New(db).RevokeUserOAuthGrants(ctx, p)
}

func (sqliteQuerier) RevokeOtherOAuthGrants(ctx context.Context, db backend.DBTX, p revokeOtherParams) ([]string, error) {
	return sqlitegen.New(db).RevokeOtherOAuthGrants(ctx, p)
}

func (sqliteQuerier) LockUserOAuthGrants(ctx context.Context, db backend.DBTX, userID string) (string, error) {
	return sqlitegen.New(db).LockUserOAuthGrants(ctx, userID)
}

func (sqliteQuerier) ListUnrevokedOAuthGrants(ctx context.Context, db backend.DBTX, userID string) ([]listGrantRow, error) {
	return sqlitegen.New(db).ListUnrevokedOAuthGrants(ctx, userID)
}

func (sqliteQuerier) DeleteDeadOAuthGrants(ctx context.Context, db backend.DBTX, p deleteDeadParams) (int64, error) {
	return sqlitegen.New(db).DeleteDeadOAuthGrants(ctx, p)
}
