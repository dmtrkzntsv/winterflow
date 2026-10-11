package serverdomain

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) ListServerDomains(ctx context.Context, db backend.DBTX, serverID string) ([]domainRow, error) {
	return sqlitegen.New(db).ListServerDomains(ctx, serverID)
}

func (sqliteQuerier) UpsertServerDomain(ctx context.Context, db backend.DBTX, p upsertParams) error {
	return sqlitegen.New(db).UpsertServerDomain(ctx, p)
}

func (sqliteQuerier) InsertServerDomain(ctx context.Context, db backend.DBTX, p insertParams) error {
	return sqlitegen.New(db).InsertServerDomain(ctx, p)
}

func (sqliteQuerier) DeleteServerDomain(ctx context.Context, db backend.DBTX, p deleteParams) error {
	return sqlitegen.New(db).DeleteServerDomain(ctx, p)
}

func (sqliteQuerier) DeleteServerDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) error {
	return sqlitegen.New(db).DeleteServerDomainsByServer(ctx, serverID)
}
