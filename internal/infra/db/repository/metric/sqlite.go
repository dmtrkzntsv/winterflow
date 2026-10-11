package metric

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) InsertMetricSample(ctx context.Context, db backend.DBTX, p insertParams) error {
	return sqlitegen.New(db).InsertMetricSample(ctx, p)
}

func (sqliteQuerier) ListMetricSamples(ctx context.Context, db backend.DBTX, p listParams) ([]sampleRow, error) {
	return sqlitegen.New(db).ListMetricSamples(ctx, p)
}

func (sqliteQuerier) ListServerAppMetricSamples(ctx context.Context, db backend.DBTX, p appsParams) ([]sampleRow, error) {
	return sqlitegen.New(db).ListServerAppMetricSamples(ctx, p)
}

func (sqliteQuerier) DeleteMetricSamplesBefore(ctx context.Context, db backend.DBTX, p pruneParams) (int64, error) {
	return sqlitegen.New(db).DeleteMetricSamplesBefore(ctx, p)
}

func (sqliteQuerier) ListAppIDsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]string, error) {
	return sqlitegen.New(db).ListAppIDsByServer(ctx, serverID)
}
