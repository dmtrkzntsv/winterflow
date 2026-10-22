package enrollment

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) InsertEnrollment(ctx context.Context, db backend.DBTX, p insertParams) error {
	return pgsqlgen.New(db).InsertEnrollment(ctx, pgsqlgen.InsertEnrollmentParams(p))
}

func (pgsqlQuerier) GetEnrollmentBySecretHash(ctx context.Context, db backend.DBTX, hash string) (row, error) {
	r, err := pgsqlgen.New(db).GetEnrollmentBySecretHash(ctx, hash)
	return row(r), err
}

func (pgsqlQuerier) GetEnrollmentByCode(ctx context.Context, db backend.DBTX, code string) (row, error) {
	r, err := pgsqlgen.New(db).GetEnrollmentByCode(ctx, &code)
	return row(r), err
}

func (pgsqlQuerier) GetEnrollmentByID(ctx context.Context, db backend.DBTX, id string) (row, error) {
	r, err := pgsqlgen.New(db).GetEnrollmentByID(ctx, id)
	return row(r), err
}

func (pgsqlQuerier) ReplaceEnrollmentCode(ctx context.Context, db backend.DBTX, p replaceParams) (int64, error) {
	return pgsqlgen.New(db).ReplaceEnrollmentCode(ctx, pgsqlgen.ReplaceEnrollmentCodeParams(p))
}

func (pgsqlQuerier) MarkEnrollmentFailed(ctx context.Context, db backend.DBTX, id string) error {
	return pgsqlgen.New(db).MarkEnrollmentFailed(ctx, id)
}

func (pgsqlQuerier) ClaimEnrollment(ctx context.Context, db backend.DBTX, p claimParams) (int64, error) {
	return pgsqlgen.New(db).ClaimEnrollment(ctx, pgsqlgen.ClaimEnrollmentParams(p))
}

func (pgsqlQuerier) InsertServerWithHub(ctx context.Context, db backend.DBTX, p serverParams) error {
	return pgsqlgen.New(db).InsertServerWithHub(ctx, pgsqlgen.InsertServerWithHubParams(p))
}

func (pgsqlQuerier) InsertCertificate(ctx context.Context, db backend.DBTX, p certParams) error {
	return pgsqlgen.New(db).InsertCertificate(ctx, pgsqlgen.InsertCertificateParams(p))
}

func (pgsqlQuerier) CountPendingEnrollmentsByIP(ctx context.Context, db backend.DBTX, ip string) (int64, error) {
	return pgsqlgen.New(db).CountPendingEnrollmentsByIP(ctx, ip)
}

func (pgsqlQuerier) CountPendingEnrollments(ctx context.Context, db backend.DBTX) (int64, error) {
	return pgsqlgen.New(db).CountPendingEnrollments(ctx)
}

func (pgsqlQuerier) PruneEnrollments(ctx context.Context, db backend.DBTX, p pruneParams) error {
	return pgsqlgen.New(db).PruneEnrollments(ctx, pgsqlgen.PruneEnrollmentsParams(p))
}
