package hubcert

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) InsertHubCertificate(ctx context.Context, db backend.DBTX, p insertParams) error {
	return sqlitegen.New(db).InsertHubCertificate(ctx, p)
}

func (sqliteQuerier) GetHubCertificate(ctx context.Context, db backend.DBTX, fingerprint string) (getRow, error) {
	return sqlitegen.New(db).GetHubCertificate(ctx, fingerprint)
}

func (sqliteQuerier) RetireOtherHubCertificates(ctx context.Context, db backend.DBTX, p retireOtherParams) error {
	return sqlitegen.New(db).RetireOtherHubCertificates(ctx, p)
}

func (sqliteQuerier) RetireHubCertificatesByHub(ctx context.Context, db backend.DBTX, p retireHubParams) (int64, error) {
	return sqlitegen.New(db).RetireHubCertificatesByHub(ctx, p)
}

func (sqliteQuerier) RetireHubCertificateByFingerprint(ctx context.Context, db backend.DBTX, p retireFpParams) (int64, error) {
	return sqlitegen.New(db).RetireHubCertificateByFingerprint(ctx, p)
}

func (sqliteQuerier) ListActiveHubCertificates(ctx context.Context, db backend.DBTX) ([]string, error) {
	return sqlitegen.New(db).ListActiveHubCertificates(ctx)
}

func (sqliteQuerier) LockHubCertificates(ctx context.Context, db backend.DBTX, hubID string) error {
	return sqlitegen.New(db).LockHubCertificates(ctx, hubID)
}

func (sqliteQuerier) CountActiveHubCertificatesByHub(ctx context.Context, db backend.DBTX, hubID string) (int64, error) {
	return sqlitegen.New(db).CountActiveHubCertificatesByHub(ctx, hubID)
}

func (sqliteQuerier) CountHubCertificatesByHub(ctx context.Context, db backend.DBTX, hubID string) (int64, error) {
	return sqlitegen.New(db).CountHubCertificatesByHub(ctx, hubID)
}
