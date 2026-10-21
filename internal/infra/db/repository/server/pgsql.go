package server

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) ListServersByUser(ctx context.Context, db backend.DBTX, userID string) ([]serverRow, error) {
	rows, err := pgsqlgen.New(db).ListServersByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]serverRow, len(rows))
	for i, row := range rows {
		out[i] = serverRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListCapabilitiesByUser(ctx context.Context, db backend.DBTX, userID string) ([]capabilityRow, error) {
	rows, err := pgsqlgen.New(db).ListCapabilitiesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]capabilityRow, len(rows))
	for i, row := range rows {
		out[i] = capabilityRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListFeaturesByUser(ctx context.Context, db backend.DBTX, userID string) ([]featureRow, error) {
	rows, err := pgsqlgen.New(db).ListFeaturesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]featureRow, len(rows))
	for i, row := range rows {
		out[i] = featureRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListServerUserIDs(ctx context.Context, db backend.DBTX, serverID string) ([]string, error) {
	return pgsqlgen.New(db).ListServerUserIDs(ctx, serverID)
}

func (pgsqlQuerier) ListOrgUserIDs(ctx context.Context, db backend.DBTX, orgID string) ([]string, error) {
	return pgsqlgen.New(db).ListOrgUserIDs(ctx, orgID)
}

func (pgsqlQuerier) UserOwnsServer(ctx context.Context, db backend.DBTX, p ownsParams) (bool, error) {
	return pgsqlgen.New(db).UserOwnsServer(ctx, pgsqlgen.UserOwnsServerParams(p))
}

func (pgsqlQuerier) ServerInOrganization(ctx context.Context, db backend.DBTX, p inOrgParams) (bool, error) {
	return pgsqlgen.New(db).ServerInOrganization(ctx, pgsqlgen.ServerInOrganizationParams(p))
}

func (pgsqlQuerier) HasAnyServer(ctx context.Context, db backend.DBTX) (bool, error) {
	return pgsqlgen.New(db).HasAnyServer(ctx)
}

func (pgsqlQuerier) GetFirstServerID(ctx context.Context, db backend.DBTX) (string, error) {
	return pgsqlgen.New(db).GetFirstServerID(ctx)
}

func (pgsqlQuerier) InsertServer(ctx context.Context, db backend.DBTX, p insertServerParams) error {
	return pgsqlgen.New(db).InsertServer(ctx, pgsqlgen.InsertServerParams(p))
}

func (pgsqlQuerier) TouchServerLastSeen(ctx context.Context, db backend.DBTX, p touchParams) error {
	return pgsqlgen.New(db).TouchServerLastSeen(ctx, pgsqlgen.TouchServerLastSeenParams(p))
}

func (pgsqlQuerier) UpsertCapability(ctx context.Context, db backend.DBTX, p capabilityParams) error {
	return pgsqlgen.New(db).UpsertCapability(ctx, pgsqlgen.UpsertCapabilityParams(p))
}

func (pgsqlQuerier) UpsertFeature(ctx context.Context, db backend.DBTX, p featureParams) error {
	return pgsqlgen.New(db).UpsertFeature(ctx, pgsqlgen.UpsertFeatureParams(p))
}

func (pgsqlQuerier) GetCapability(ctx context.Context, db backend.DBTX, p getCapParams) (string, error) {
	return pgsqlgen.New(db).GetCapability(ctx, pgsqlgen.GetCapabilityParams(p))
}

func (pgsqlQuerier) InsertRegistration(ctx context.Context, db backend.DBTX, p registrationParams) error {
	return pgsqlgen.New(db).InsertRegistration(ctx, pgsqlgen.InsertRegistrationParams(p))
}

func (pgsqlQuerier) DeleteRegistration(ctx context.Context, db backend.DBTX, serverID string) error {
	return pgsqlgen.New(db).DeleteRegistration(ctx, serverID)
}

func (pgsqlQuerier) DeleteAllRegistrations(ctx context.Context, db backend.DBTX) error {
	return pgsqlgen.New(db).DeleteAllRegistrations(ctx)
}

func (pgsqlQuerier) GetRegistrationByCode(ctx context.Context, db backend.DBTX, code string) (registrationRow, error) {
	row, err := pgsqlgen.New(db).GetRegistrationByCode(ctx, code)
	return registrationRow(row), err
}

func (pgsqlQuerier) GetLatestRegistrationCode(ctx context.Context, db backend.DBTX) (string, error) {
	return pgsqlgen.New(db).GetLatestRegistrationCode(ctx)
}

func (pgsqlQuerier) InsertCertificate(ctx context.Context, db backend.DBTX, p certificateParams) error {
	return pgsqlgen.New(db).InsertCertificate(ctx, pgsqlgen.InsertCertificateParams(p))
}

func (pgsqlQuerier) GetServerHub(ctx context.Context, db backend.DBTX, serverID string) (*string, error) {
	return pgsqlgen.New(db).GetServerHub(ctx, serverID)
}

func (pgsqlQuerier) SetServerHub(ctx context.Context, db backend.DBTX, p setHubParams) (int64, error) {
	return pgsqlgen.New(db).SetServerHub(ctx, pgsqlgen.SetServerHubParams(p))
}

func (pgsqlQuerier) CountServersByHub(ctx context.Context, db backend.DBTX) ([]hubCountRow, error) {
	rows, err := pgsqlgen.New(db).CountServersByHub(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]hubCountRow, len(rows))
	for i, row := range rows {
		out[i] = hubCountRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) DeleteServerRow(ctx context.Context, db backend.DBTX, serverID string) error {
	return pgsqlgen.New(db).DeleteServerRow(ctx, serverID)
}

func (pgsqlQuerier) DeleteServerDomainRows(ctx context.Context, db backend.DBTX, serverID string) error {
	return pgsqlgen.New(db).DeleteServerDomainRows(ctx, serverID)
}

func (pgsqlQuerier) DeleteServerEnrollments(ctx context.Context, db backend.DBTX, serverID string) error {
	return pgsqlgen.New(db).DeleteServerEnrollments(ctx, &serverID)
}

func (pgsqlQuerier) GetActiveServerCertificate(ctx context.Context, db backend.DBTX, serverID string) (string, error) {
	return pgsqlgen.New(db).GetActiveServerCertificate(ctx, serverID)
}
