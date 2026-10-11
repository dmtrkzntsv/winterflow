package appdomain

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) GetDomainClaim(ctx context.Context, db backend.DBTX, p claimParams) (claimRow, error) {
	return sqlitegen.New(db).GetDomainClaim(ctx, p)
}

func (sqliteQuerier) InsertAppDomain(ctx context.Context, db backend.DBTX, p insertParams) error {
	return sqlitegen.New(db).InsertAppDomain(ctx, p)
}

func (sqliteQuerier) DeleteAppDomainsByApp(ctx context.Context, db backend.DBTX, appID string) error {
	return sqlitegen.New(db).DeleteAppDomainsByApp(ctx, appID)
}

func (sqliteQuerier) DeleteAppDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) error {
	return sqlitegen.New(db).DeleteAppDomainsByServer(ctx, serverID)
}

func (sqliteQuerier) ListAppDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]domainRow, error) {
	return sqlitegen.New(db).ListAppDomainsByServer(ctx, serverID)
}

func (sqliteQuerier) ListAppDomainNames(ctx context.Context, db backend.DBTX, p namesParams) ([]string, error) {
	return sqlitegen.New(db).ListAppDomainNames(ctx, p)
}
