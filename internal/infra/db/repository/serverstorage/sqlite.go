package serverstorage

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) ListServerStorages(ctx context.Context, db backend.DBTX, serverID string) ([]storageRow, error) {
	return sqlitegen.New(db).ListServerStorages(ctx, serverID)
}

func (sqliteQuerier) UpsertServerStorage(ctx context.Context, db backend.DBTX, p upsertParams) error {
	return sqlitegen.New(db).UpsertServerStorage(ctx, p)
}

func (sqliteQuerier) InsertServerStorage(ctx context.Context, db backend.DBTX, p insertParams) error {
	return sqlitegen.New(db).InsertServerStorage(ctx, p)
}

func (sqliteQuerier) DeleteServerStorage(ctx context.Context, db backend.DBTX, p deleteParams) error {
	return sqlitegen.New(db).DeleteServerStorage(ctx, p)
}

func (sqliteQuerier) DeleteServerStoragesByServer(ctx context.Context, db backend.DBTX, serverID string) error {
	return sqlitegen.New(db).DeleteServerStoragesByServer(ctx, serverID)
}
