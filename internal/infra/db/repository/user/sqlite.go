package user

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) GetUser(ctx context.Context, db backend.DBTX, userID string) (userRow, error) {
	return sqlitegen.New(db).GetUser(ctx, userID)
}

func (sqliteQuerier) CountUsers(ctx context.Context, db backend.DBTX) (int64, error) {
	return sqlitegen.New(db).CountUsers(ctx)
}

func (sqliteQuerier) InsertUser(ctx context.Context, db backend.DBTX, p insertUserParams) error {
	return sqlitegen.New(db).InsertUser(ctx, p)
}

func (sqliteQuerier) DeleteUser(ctx context.Context, db backend.DBTX, userID string) error {
	return sqlitegen.New(db).DeleteUser(ctx, userID)
}

func (sqliteQuerier) GetConnectedAccountUserID(ctx context.Context, db backend.DBTX, p accountKeyParams) (string, error) {
	return sqlitegen.New(db).GetConnectedAccountUserID(ctx, p)
}

func (sqliteQuerier) InsertConnectedAccount(ctx context.Context, db backend.DBTX, p accountParams) error {
	return sqlitegen.New(db).InsertConnectedAccount(ctx, p)
}

func (sqliteQuerier) GetFirstConnectedAccountProvider(ctx context.Context, db backend.DBTX, userID string) (string, error) {
	return sqlitegen.New(db).GetFirstConnectedAccountProvider(ctx, userID)
}

func (sqliteQuerier) InsertCredentials(ctx context.Context, db backend.DBTX, p credentialsParams) error {
	return sqlitegen.New(db).InsertCredentials(ctx, p)
}

func (sqliteQuerier) GetCredentialsByEmail(ctx context.Context, db backend.DBTX, email string) (credentialsRow, error) {
	return sqlitegen.New(db).GetCredentialsByEmail(ctx, email)
}

func (sqliteQuerier) GetCredentialsByUser(ctx context.Context, db backend.DBTX, userID string) (credentialsRow, error) {
	return sqlitegen.New(db).GetCredentialsByUser(ctx, userID)
}

func (sqliteQuerier) EmailTaken(ctx context.Context, db backend.DBTX, email string) (bool, error) {
	return sqlitegen.New(db).EmailTaken(ctx, email)
}

func (sqliteQuerier) UpdatePassword(ctx context.Context, db backend.DBTX, p passwordParams) (int64, error) {
	return sqlitegen.New(db).UpdatePassword(ctx, p)
}

func (sqliteQuerier) InsertOrganization(ctx context.Context, db backend.DBTX, p insertOrgParams) error {
	return sqlitegen.New(db).InsertOrganization(ctx, p)
}

func (sqliteQuerier) GetOrganization(ctx context.Context, db backend.DBTX, organizationID string) (organizationRow, error) {
	return sqlitegen.New(db).GetOrganization(ctx, organizationID)
}

func (sqliteQuerier) UpdateOrganization(ctx context.Context, db backend.DBTX, p updateOrgParams) (int64, error) {
	return sqlitegen.New(db).UpdateOrganization(ctx, p)
}

func (sqliteQuerier) InsertOrganizationUser(ctx context.Context, db backend.DBTX, p membershipParams) error {
	return sqlitegen.New(db).InsertOrganizationUser(ctx, p)
}

func (sqliteQuerier) GetPrimaryOrganizationID(ctx context.Context, db backend.DBTX, userID string) (string, error) {
	return sqlitegen.New(db).GetPrimaryOrganizationID(ctx, userID)
}

func (sqliteQuerier) LockOrganizationMember(ctx context.Context, db backend.DBTX, p memberKeyParams) (string, error) {
	return sqlitegen.New(db).LockOrganizationMember(ctx, p)
}

func (sqliteQuerier) IsOrganizationMember(ctx context.Context, db backend.DBTX, p isMemberParams) (bool, error) {
	return sqlitegen.New(db).IsOrganizationMember(ctx, p)
}

func (sqliteQuerier) ListOrganizationMembers(ctx context.Context, db backend.DBTX, organizationID string) ([]userRow, error) {
	return sqlitegen.New(db).ListOrganizationMembers(ctx, organizationID)
}
