package app

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) ListAppsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]appRow, error) {
	rows, err := pgsqlgen.New(db).ListAppsByServer(ctx, serverID)
	if err != nil {
		return nil, err
	}
	out := make([]appRow, len(rows))
	for i, row := range rows {
		out[i] = appRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListAppIDsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]string, error) {
	return pgsqlgen.New(db).ListAppIDsByServer(ctx, serverID)
}

func (pgsqlQuerier) GetApp(ctx context.Context, db backend.DBTX, appID string) (appRow, error) {
	row, err := pgsqlgen.New(db).GetApp(ctx, appID)
	return appRow(row), err
}

func (pgsqlQuerier) UpsertApp(ctx context.Context, db backend.DBTX, p upsertParams) error {
	return pgsqlgen.New(db).UpsertApp(ctx, pgsqlgen.UpsertAppParams(p))
}

func (pgsqlQuerier) DeleteApp(ctx context.Context, db backend.DBTX, appID string) error {
	return pgsqlgen.New(db).DeleteApp(ctx, appID)
}

func (pgsqlQuerier) RenameApp(ctx context.Context, db backend.DBTX, p renameParams) error {
	return pgsqlgen.New(db).RenameApp(ctx, pgsqlgen.RenameAppParams(p))
}

func (pgsqlQuerier) DeleteMetricSamplesByApp(ctx context.Context, db backend.DBTX, appID string) error {
	return pgsqlgen.New(db).DeleteMetricSamplesByApp(ctx, appID)
}
