package oauthlogin

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) GetIdentityByProviderSubject(ctx context.Context, db backend.DBTX, p subjectParams) (identityRow, error) {
	return sqlitegen.New(db).GetIdentityByProviderSubject(ctx, p)
}

func (sqliteQuerier) GetIdentityByUserProvider(ctx context.Context, db backend.DBTX, p userProvParams) (identityRow, error) {
	return sqlitegen.New(db).GetIdentityByUserProvider(ctx, p)
}

func (sqliteQuerier) ListIdentitiesByUser(ctx context.Context, db backend.DBTX, userID string) ([]identityRow, error) {
	return sqlitegen.New(db).ListIdentitiesByUser(ctx, userID)
}

func (sqliteQuerier) CountIdentitiesByUser(ctx context.Context, db backend.DBTX, userID string) (int64, error) {
	return sqlitegen.New(db).CountIdentitiesByUser(ctx, userID)
}

func (sqliteQuerier) UpdateIdentityEmail(ctx context.Context, db backend.DBTX, p emailParams) (int64, error) {
	return sqlitegen.New(db).UpdateIdentityEmail(ctx, p)
}

func (sqliteQuerier) RekeyIdentity(ctx context.Context, db backend.DBTX, p rekeyParams) (int64, error) {
	return sqlitegen.New(db).RekeyIdentity(ctx, p)
}

func (sqliteQuerier) InsertIdentityIfAbsent(ctx context.Context, db backend.DBTX, p insertIDParams) (int64, error) {
	return sqlitegen.New(db).InsertIdentityIfAbsent(ctx, p)
}

func (sqliteQuerier) DeleteIdentityByUserProvider(ctx context.Context, db backend.DBTX, p deleteIDParams) (int64, error) {
	return sqlitegen.New(db).DeleteIdentityByUserProvider(ctx, p)
}

func (sqliteQuerier) LockUserIdentities(ctx context.Context, db backend.DBTX, userID string) (string, error) {
	return sqlitegen.New(db).LockUserIdentities(ctx, userID)
}

func (sqliteQuerier) UserHasPassword(ctx context.Context, db backend.DBTX, userID string) (bool, error) {
	return sqlitegen.New(db).UserHasPassword(ctx, userID)
}

func (sqliteQuerier) SignInEmailInUse(ctx context.Context, db backend.DBTX, address string) (bool, error) {
	return sqlitegen.New(db).SignInEmailInUse(ctx, address)
}

func (sqliteQuerier) InsertOAuthLoginState(ctx context.Context, db backend.DBTX, p insertStateParams) error {
	return sqlitegen.New(db).InsertOAuthLoginState(ctx, p)
}

func (sqliteQuerier) ConsumeOAuthLoginState(ctx context.Context, db backend.DBTX, stateHash string) (stateRow, error) {
	return sqlitegen.New(db).ConsumeOAuthLoginState(ctx, stateHash)
}

func (sqliteQuerier) DeleteExpiredOAuthLoginStates(ctx context.Context, db backend.DBTX, before types.DateTime) (int64, error) {
	return sqlitegen.New(db).DeleteExpiredOAuthLoginStates(ctx, before)
}
