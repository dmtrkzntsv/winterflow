package hubcert

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) InsertHubCertificate(ctx context.Context, db backend.DBTX, p insertParams) error {
	return pgsqlgen.New(db).InsertHubCertificate(ctx, pgsqlgen.InsertHubCertificateParams(p))
}

func (pgsqlQuerier) GetHubCertificate(ctx context.Context, db backend.DBTX, fingerprint string) (getRow, error) {
	row, err := pgsqlgen.New(db).GetHubCertificate(ctx, fingerprint)
	return getRow(row), err
}

func (pgsqlQuerier) RetireOtherHubCertificates(ctx context.Context, db backend.DBTX, p retireOtherParams) error {
	return pgsqlgen.New(db).RetireOtherHubCertificates(ctx, pgsqlgen.RetireOtherHubCertificatesParams(p))
}

func (pgsqlQuerier) RetireHubCertificatesByHub(ctx context.Context, db backend.DBTX, p retireHubParams) (int64, error) {
	return pgsqlgen.New(db).RetireHubCertificatesByHub(ctx, pgsqlgen.RetireHubCertificatesByHubParams(p))
}

func (pgsqlQuerier) RetireHubCertificateByFingerprint(ctx context.Context, db backend.DBTX, p retireFpParams) (int64, error) {
	return pgsqlgen.New(db).RetireHubCertificateByFingerprint(ctx, pgsqlgen.RetireHubCertificateByFingerprintParams(p))
}

func (pgsqlQuerier) ListActiveHubCertificates(ctx context.Context, db backend.DBTX) ([]string, error) {
	return pgsqlgen.New(db).ListActiveHubCertificates(ctx)
}

func (pgsqlQuerier) LockHubCertificates(ctx context.Context, db backend.DBTX, hubID string) error {
	return pgsqlgen.New(db).LockHubCertificates(ctx, hubID)
}

func (pgsqlQuerier) CountActiveHubCertificatesByHub(ctx context.Context, db backend.DBTX, hubID string) (int64, error) {
	return pgsqlgen.New(db).CountActiveHubCertificatesByHub(ctx, hubID)
}

func (pgsqlQuerier) CountHubCertificatesByHub(ctx context.Context, db backend.DBTX, hubID string) (int64, error) {
	return pgsqlgen.New(db).CountHubCertificatesByHub(ctx, hubID)
}
