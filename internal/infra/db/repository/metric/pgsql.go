package metric

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) InsertMetricSample(ctx context.Context, db backend.DBTX, p insertParams) error {
	return pgsqlgen.New(db).InsertMetricSample(ctx, pgsqlgen.InsertMetricSampleParams(p))
}

func (pgsqlQuerier) ListMetricSamples(ctx context.Context, db backend.DBTX, p listParams) ([]sampleRow, error) {
	return convert(pgsqlgen.New(db).ListMetricSamples(ctx, pgsqlgen.ListMetricSamplesParams(p)))
}

func (pgsqlQuerier) ListServerAppMetricSamples(ctx context.Context, db backend.DBTX, p appsParams) ([]sampleRow, error) {
	return convert(pgsqlgen.New(db).ListServerAppMetricSamples(ctx, pgsqlgen.ListServerAppMetricSamplesParams(p)))
}

func (pgsqlQuerier) DeleteMetricSamplesBefore(ctx context.Context, db backend.DBTX, p pruneParams) (int64, error) {
	return pgsqlgen.New(db).DeleteMetricSamplesBefore(ctx, pgsqlgen.DeleteMetricSamplesBeforeParams(p))
}

func (pgsqlQuerier) ListMetricSamplesInRange(ctx context.Context, db backend.DBTX, p rangeParams) ([]sampleRow, error) {
	return convert(pgsqlgen.New(db).ListMetricSamplesInRange(ctx, pgsqlgen.ListMetricSamplesInRangeParams(p)))
}

func (pgsqlQuerier) ListAppIDsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]string, error) {
	return pgsqlgen.New(db).ListAppIDsByServer(ctx, serverID)
}

func convert(rows []pgsqlgen.MetricSample, err error) ([]sampleRow, error) {
	if err != nil {
		return nil, err
	}
	out := make([]sampleRow, len(rows))
	for i, row := range rows {
		out[i] = sampleRow(row)
	}
	return out, nil
}
