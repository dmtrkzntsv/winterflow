package server

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) ListServersByUser(ctx context.Context, db backend.DBTX, userID string) ([]serverRow, error) {
	return sqlitegen.New(db).ListServersByUser(ctx, userID)
}

func (sqliteQuerier) ListCapabilitiesByUser(ctx context.Context, db backend.DBTX, userID string) ([]capabilityRow, error) {
	return sqlitegen.New(db).ListCapabilitiesByUser(ctx, userID)
}

func (sqliteQuerier) ListFeaturesByUser(ctx context.Context, db backend.DBTX, userID string) ([]featureRow, error) {
	return sqlitegen.New(db).ListFeaturesByUser(ctx, userID)
}

func (sqliteQuerier) ListServerUserIDs(ctx context.Context, db backend.DBTX, serverID string) ([]string, error) {
	return sqlitegen.New(db).ListServerUserIDs(ctx, serverID)
}

func (sqliteQuerier) ListOrgUserIDs(ctx context.Context, db backend.DBTX, orgID string) ([]string, error) {
	return sqlitegen.New(db).ListOrgUserIDs(ctx, orgID)
}

func (sqliteQuerier) UserOwnsServer(ctx context.Context, db backend.DBTX, p ownsParams) (bool, error) {
	return sqlitegen.New(db).UserOwnsServer(ctx, p)
}

func (sqliteQuerier) ServerInOrganization(ctx context.Context, db backend.DBTX, p inOrgParams) (bool, error) {
	return sqlitegen.New(db).ServerInOrganization(ctx, p)
}

func (sqliteQuerier) HasAnyServer(ctx context.Context, db backend.DBTX) (bool, error) {
	return sqlitegen.New(db).HasAnyServer(ctx)
}

func (sqliteQuerier) GetFirstServerID(ctx context.Context, db backend.DBTX) (string, error) {
	return sqlitegen.New(db).GetFirstServerID(ctx)
}

func (sqliteQuerier) InsertServer(ctx context.Context, db backend.DBTX, p insertServerParams) error {
	return sqlitegen.New(db).InsertServer(ctx, p)
}

func (sqliteQuerier) TouchServerLastSeen(ctx context.Context, db backend.DBTX, p touchParams) error {
	return sqlitegen.New(db).TouchServerLastSeen(ctx, p)
}

func (sqliteQuerier) UpsertCapability(ctx context.Context, db backend.DBTX, p capabilityParams) error {
	return sqlitegen.New(db).UpsertCapability(ctx, p)
}

func (sqliteQuerier) UpsertFeature(ctx context.Context, db backend.DBTX, p featureParams) error {
	return sqlitegen.New(db).UpsertFeature(ctx, p)
}

func (sqliteQuerier) GetCapability(ctx context.Context, db backend.DBTX, p getCapParams) (string, error) {
	return sqlitegen.New(db).GetCapability(ctx, p)
}

func (sqliteQuerier) InsertRegistration(ctx context.Context, db backend.DBTX, p registrationParams) error {
	return sqlitegen.New(db).InsertRegistration(ctx, p)
}

func (sqliteQuerier) DeleteRegistration(ctx context.Context, db backend.DBTX, serverID string) error {
	return sqlitegen.New(db).DeleteRegistration(ctx, serverID)
}

func (sqliteQuerier) DeleteAllRegistrations(ctx context.Context, db backend.DBTX) error {
	return sqlitegen.New(db).DeleteAllRegistrations(ctx)
}

func (sqliteQuerier) GetRegistrationByCode(ctx context.Context, db backend.DBTX, code string) (registrationRow, error) {
	return sqlitegen.New(db).GetRegistrationByCode(ctx, code)
}

func (sqliteQuerier) GetLatestRegistrationCode(ctx context.Context, db backend.DBTX) (string, error) {
	return sqlitegen.New(db).GetLatestRegistrationCode(ctx)
}

func (sqliteQuerier) InsertCertificate(ctx context.Context, db backend.DBTX, p certificateParams) error {
	return sqlitegen.New(db).InsertCertificate(ctx, p)
}

func (sqliteQuerier) GetServerHub(ctx context.Context, db backend.DBTX, serverID string) (*string, error) {
	return sqlitegen.New(db).GetServerHub(ctx, serverID)
}

func (sqliteQuerier) SetServerHub(ctx context.Context, db backend.DBTX, p setHubParams) (int64, error) {
	return sqlitegen.New(db).SetServerHub(ctx, p)
}

func (sqliteQuerier) CountServersByHub(ctx context.Context, db backend.DBTX) ([]hubCountRow, error) {
	return sqlitegen.New(db).CountServersByHub(ctx)
}

func (sqliteQuerier) DeleteServerRow(ctx context.Context, db backend.DBTX, serverID string) error {
	return sqlitegen.New(db).DeleteServerRow(ctx, serverID)
}

func (sqliteQuerier) DeleteServerDomainRows(ctx context.Context, db backend.DBTX, serverID string) error {
	return sqlitegen.New(db).DeleteServerDomainRows(ctx, serverID)
}

func (sqliteQuerier) DeleteServerEnrollments(ctx context.Context, db backend.DBTX, serverID string) error {
	return sqlitegen.New(db).DeleteServerEnrollments(ctx, &serverID)
}

func (sqliteQuerier) GetActiveServerCertificate(ctx context.Context, db backend.DBTX, serverID string) (string, error) {
	return sqlitegen.New(db).GetActiveServerCertificate(ctx, serverID)
}
