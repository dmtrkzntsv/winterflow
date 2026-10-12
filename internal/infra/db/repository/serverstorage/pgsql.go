package serverstorage

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) ListServerStorages(ctx context.Context, db backend.DBTX, serverID string) ([]storageRow, error) {
	rows, err := pgsqlgen.New(db).ListServerStorages(ctx, serverID)
	if err != nil {
		return nil, err
	}
	out := make([]storageRow, len(rows))
	for i, row := range rows {
		out[i] = storageRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) UpsertServerStorage(ctx context.Context, db backend.DBTX, p upsertParams) error {
	return pgsqlgen.New(db).UpsertServerStorage(ctx, pgsqlgen.UpsertServerStorageParams(p))
}

func (pgsqlQuerier) InsertServerStorage(ctx context.Context, db backend.DBTX, p insertParams) error {
	return pgsqlgen.New(db).InsertServerStorage(ctx, pgsqlgen.InsertServerStorageParams(p))
}

func (pgsqlQuerier) DeleteServerStorage(ctx context.Context, db backend.DBTX, p deleteParams) error {
	return pgsqlgen.New(db).DeleteServerStorage(ctx, pgsqlgen.DeleteServerStorageParams(p))
}

func (pgsqlQuerier) DeleteServerStoragesByServer(ctx context.Context, db backend.DBTX, serverID string) error {
	return pgsqlgen.New(db).DeleteServerStoragesByServer(ctx, serverID)
}
