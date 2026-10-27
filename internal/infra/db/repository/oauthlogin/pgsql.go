package oauthlogin

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
	"winterflow/internal/infra/db/types"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) GetIdentityByProviderSubject(ctx context.Context, db backend.DBTX, p subjectParams) (identityRow, error) {
	row, err := pgsqlgen.New(db).GetIdentityByProviderSubject(ctx, pgsqlgen.GetIdentityByProviderSubjectParams(p))
	return identityRow(row), err
}

func (pgsqlQuerier) GetIdentityByUserProvider(ctx context.Context, db backend.DBTX, p userProvParams) (identityRow, error) {
	row, err := pgsqlgen.New(db).GetIdentityByUserProvider(ctx, pgsqlgen.GetIdentityByUserProviderParams(p))
	return identityRow(row), err
}

func (pgsqlQuerier) ListIdentitiesByUser(ctx context.Context, db backend.DBTX, userID string) ([]identityRow, error) {
	rows, err := pgsqlgen.New(db).ListIdentitiesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]identityRow, len(rows))
	for i, row := range rows {
		out[i] = identityRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) CountIdentitiesByUser(ctx context.Context, db backend.DBTX, userID string) (int64, error) {
	return pgsqlgen.New(db).CountIdentitiesByUser(ctx, userID)
}

func (pgsqlQuerier) UpdateIdentityEmail(ctx context.Context, db backend.DBTX, p emailParams) (int64, error) {
	return pgsqlgen.New(db).UpdateIdentityEmail(ctx, pgsqlgen.UpdateIdentityEmailParams(p))
}

func (pgsqlQuerier) RekeyIdentity(ctx context.Context, db backend.DBTX, p rekeyParams) (int64, error) {
	return pgsqlgen.New(db).RekeyIdentity(ctx, pgsqlgen.RekeyIdentityParams(p))
}

func (pgsqlQuerier) InsertIdentityIfAbsent(ctx context.Context, db backend.DBTX, p insertIDParams) (int64, error) {
	return pgsqlgen.New(db).InsertIdentityIfAbsent(ctx, pgsqlgen.InsertIdentityIfAbsentParams(p))
}

func (pgsqlQuerier) DeleteIdentityByUserProvider(ctx context.Context, db backend.DBTX, p deleteIDParams) (int64, error) {
	return pgsqlgen.New(db).DeleteIdentityByUserProvider(ctx, pgsqlgen.DeleteIdentityByUserProviderParams(p))
}

func (pgsqlQuerier) LockUserIdentities(ctx context.Context, db backend.DBTX, userID string) (string, error) {
	return pgsqlgen.New(db).LockUserIdentities(ctx, userID)
}

func (pgsqlQuerier) UserHasPassword(ctx context.Context, db backend.DBTX, userID string) (bool, error) {
	return pgsqlgen.New(db).UserHasPassword(ctx, userID)
}

func (pgsqlQuerier) SignInEmailInUse(ctx context.Context, db backend.DBTX, address string) (bool, error) {
	return pgsqlgen.New(db).SignInEmailInUse(ctx, address)
}

func (pgsqlQuerier) InsertOAuthLoginState(ctx context.Context, db backend.DBTX, p insertStateParams) error {
	return pgsqlgen.New(db).InsertOAuthLoginState(ctx, pgsqlgen.InsertOAuthLoginStateParams(p))
}

func (pgsqlQuerier) ConsumeOAuthLoginState(ctx context.Context, db backend.DBTX, stateHash string) (stateRow, error) {
	row, err := pgsqlgen.New(db).ConsumeOAuthLoginState(ctx, stateHash)
	return stateRow(row), err
}

func (pgsqlQuerier) DeleteExpiredOAuthLoginStates(ctx context.Context, db backend.DBTX, before types.DateTime) (int64, error) {
	return pgsqlgen.New(db).DeleteExpiredOAuthLoginStates(ctx, before)
}
