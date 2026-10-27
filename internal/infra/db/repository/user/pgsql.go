package user

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) GetUser(ctx context.Context, db backend.DBTX, userID string) (userRow, error) {
	row, err := pgsqlgen.New(db).GetUser(ctx, userID)
	return userRow(row), err
}

func (pgsqlQuerier) CountUsers(ctx context.Context, db backend.DBTX) (int64, error) {
	return pgsqlgen.New(db).CountUsers(ctx)
}

func (pgsqlQuerier) InsertUser(ctx context.Context, db backend.DBTX, p insertUserParams) error {
	return pgsqlgen.New(db).InsertUser(ctx, pgsqlgen.InsertUserParams(p))
}

func (pgsqlQuerier) DeleteUser(ctx context.Context, db backend.DBTX, userID string) error {
	return pgsqlgen.New(db).DeleteUser(ctx, userID)
}

func (pgsqlQuerier) GetConnectedAccountUserID(ctx context.Context, db backend.DBTX, p accountKeyParams) (string, error) {
	return pgsqlgen.New(db).GetConnectedAccountUserID(ctx, pgsqlgen.GetConnectedAccountUserIDParams(p))
}

func (pgsqlQuerier) InsertIdentity(ctx context.Context, db backend.DBTX, p identityParams) error {
	return pgsqlgen.New(db).InsertIdentity(ctx, pgsqlgen.InsertIdentityParams(p))
}

func (pgsqlQuerier) InsertConnectedAccount(ctx context.Context, db backend.DBTX, p accountParams) error {
	return pgsqlgen.New(db).InsertConnectedAccount(ctx, pgsqlgen.InsertConnectedAccountParams(p))
}

func (pgsqlQuerier) GetFirstConnectedAccountProvider(ctx context.Context, db backend.DBTX, userID string) (string, error) {
	return pgsqlgen.New(db).GetFirstConnectedAccountProvider(ctx, userID)
}

func (pgsqlQuerier) InsertCredentials(ctx context.Context, db backend.DBTX, p credentialsParams) error {
	return pgsqlgen.New(db).InsertCredentials(ctx, pgsqlgen.InsertCredentialsParams(p))
}

func (pgsqlQuerier) GetCredentialsByEmail(ctx context.Context, db backend.DBTX, email string) (credentialsRow, error) {
	row, err := pgsqlgen.New(db).GetCredentialsByEmail(ctx, email)
	return credentialsRow(row), err
}

func (pgsqlQuerier) GetCredentialsByUser(ctx context.Context, db backend.DBTX, userID string) (credentialsRow, error) {
	row, err := pgsqlgen.New(db).GetCredentialsByUser(ctx, userID)
	return credentialsRow(row), err
}

func (pgsqlQuerier) EmailTaken(ctx context.Context, db backend.DBTX, email string) (bool, error) {
	return pgsqlgen.New(db).EmailTaken(ctx, email)
}

func (pgsqlQuerier) UpdatePassword(ctx context.Context, db backend.DBTX, p passwordParams) (int64, error) {
	return pgsqlgen.New(db).UpdatePassword(ctx, pgsqlgen.UpdatePasswordParams(p))
}

func (pgsqlQuerier) RehashPassword(ctx context.Context, db backend.DBTX, p rehashParams) (int64, error) {
	return pgsqlgen.New(db).RehashPassword(ctx, pgsqlgen.RehashPasswordParams(p))
}

func (pgsqlQuerier) InsertOrganization(ctx context.Context, db backend.DBTX, p insertOrgParams) error {
	return pgsqlgen.New(db).InsertOrganization(ctx, pgsqlgen.InsertOrganizationParams(p))
}

func (pgsqlQuerier) GetOrganization(ctx context.Context, db backend.DBTX, organizationID string) (organizationRow, error) {
	row, err := pgsqlgen.New(db).GetOrganization(ctx, organizationID)
	return organizationRow(row), err
}

func (pgsqlQuerier) UpdateOrganization(ctx context.Context, db backend.DBTX, p updateOrgParams) (int64, error) {
	return pgsqlgen.New(db).UpdateOrganization(ctx, pgsqlgen.UpdateOrganizationParams(p))
}

func (pgsqlQuerier) InsertOrganizationUser(ctx context.Context, db backend.DBTX, p membershipParams) error {
	return pgsqlgen.New(db).InsertOrganizationUser(ctx, pgsqlgen.InsertOrganizationUserParams(p))
}

func (pgsqlQuerier) GetPrimaryOrganizationID(ctx context.Context, db backend.DBTX, userID string) (string, error) {
	return pgsqlgen.New(db).GetPrimaryOrganizationID(ctx, userID)
}

func (pgsqlQuerier) LockOrganizationMember(ctx context.Context, db backend.DBTX, p memberKeyParams) (string, error) {
	return pgsqlgen.New(db).LockOrganizationMember(ctx, pgsqlgen.LockOrganizationMemberParams(p))
}

func (pgsqlQuerier) IsOrganizationMember(ctx context.Context, db backend.DBTX, p isMemberParams) (bool, error) {
	return pgsqlgen.New(db).IsOrganizationMember(ctx, pgsqlgen.IsOrganizationMemberParams(p))
}

func (pgsqlQuerier) ListOrganizationMembers(ctx context.Context, db backend.DBTX, organizationID string) ([]userRow, error) {
	rows, err := pgsqlgen.New(db).ListOrganizationMembers(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	out := make([]userRow, len(rows))
	for i, row := range rows {
		out[i] = userRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) LockUserCreation(ctx context.Context, db backend.DBTX) error {
	return pgsqlgen.New(db).LockUserCreation(ctx)
}
