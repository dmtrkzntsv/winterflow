package enrollment

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) InsertEnrollment(ctx context.Context, db backend.DBTX, p insertParams) error {
	return sqlitegen.New(db).InsertEnrollment(ctx, p)
}

func (sqliteQuerier) GetEnrollmentBySecretHash(ctx context.Context, db backend.DBTX, hash string) (row, error) {
	return sqlitegen.New(db).GetEnrollmentBySecretHash(ctx, hash)
}

func (sqliteQuerier) GetEnrollmentByCode(ctx context.Context, db backend.DBTX, code string) (row, error) {
	return sqlitegen.New(db).GetEnrollmentByCode(ctx, &code)
}

func (sqliteQuerier) GetEnrollmentByID(ctx context.Context, db backend.DBTX, id string) (row, error) {
	return sqlitegen.New(db).GetEnrollmentByID(ctx, id)
}

func (sqliteQuerier) ReplaceEnrollmentCode(ctx context.Context, db backend.DBTX, p replaceParams) (int64, error) {
	return sqlitegen.New(db).ReplaceEnrollmentCode(ctx, p)
}

func (sqliteQuerier) MarkEnrollmentFailed(ctx context.Context, db backend.DBTX, id string) error {
	return sqlitegen.New(db).MarkEnrollmentFailed(ctx, id)
}

func (sqliteQuerier) ClaimEnrollment(ctx context.Context, db backend.DBTX, p claimParams) (int64, error) {
	return sqlitegen.New(db).ClaimEnrollment(ctx, p)
}

func (sqliteQuerier) InsertServerWithHub(ctx context.Context, db backend.DBTX, p serverParams) error {
	return sqlitegen.New(db).InsertServerWithHub(ctx, p)
}

func (sqliteQuerier) InsertCertificate(ctx context.Context, db backend.DBTX, p certParams) error {
	return sqlitegen.New(db).InsertCertificate(ctx, p)
}

func (sqliteQuerier) CountPendingEnrollmentsByIP(ctx context.Context, db backend.DBTX, ip string) (int64, error) {
	return sqlitegen.New(db).CountPendingEnrollmentsByIP(ctx, ip)
}

func (sqliteQuerier) CountPendingEnrollments(ctx context.Context, db backend.DBTX) (int64, error) {
	return sqlitegen.New(db).CountPendingEnrollments(ctx)
}

func (sqliteQuerier) PruneEnrollments(ctx context.Context, db backend.DBTX, p pruneParams) error {
	return sqlitegen.New(db).PruneEnrollments(ctx, p)
}
