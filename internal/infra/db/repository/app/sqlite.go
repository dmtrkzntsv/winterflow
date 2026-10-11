package app

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) ListAppsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]appRow, error) {
	return sqlitegen.New(db).ListAppsByServer(ctx, serverID)
}

func (sqliteQuerier) ListAppIDsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]string, error) {
	return sqlitegen.New(db).ListAppIDsByServer(ctx, serverID)
}

func (sqliteQuerier) GetApp(ctx context.Context, db backend.DBTX, appID string) (appRow, error) {
	return sqlitegen.New(db).GetApp(ctx, appID)
}

func (sqliteQuerier) UpsertApp(ctx context.Context, db backend.DBTX, p upsertParams) error {
	return sqlitegen.New(db).UpsertApp(ctx, p)
}

func (sqliteQuerier) DeleteApp(ctx context.Context, db backend.DBTX, appID string) error {
	return sqlitegen.New(db).DeleteApp(ctx, appID)
}

func (sqliteQuerier) RenameApp(ctx context.Context, db backend.DBTX, p renameParams) error {
	return sqlitegen.New(db).RenameApp(ctx, p)
}

func (sqliteQuerier) DeleteMetricSamplesByApp(ctx context.Context, db backend.DBTX, appID string) error {
	return sqlitegen.New(db).DeleteMetricSamplesByApp(ctx, appID)
}
