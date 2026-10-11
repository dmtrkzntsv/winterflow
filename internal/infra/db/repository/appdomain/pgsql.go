package appdomain

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) GetDomainClaim(ctx context.Context, db backend.DBTX, p claimParams) (claimRow, error) {
	row, err := pgsqlgen.New(db).GetDomainClaim(ctx, pgsqlgen.GetDomainClaimParams(p))
	return claimRow(row), err
}

func (pgsqlQuerier) InsertAppDomain(ctx context.Context, db backend.DBTX, p insertParams) error {
	return pgsqlgen.New(db).InsertAppDomain(ctx, pgsqlgen.InsertAppDomainParams(p))
}

func (pgsqlQuerier) DeleteAppDomainsByApp(ctx context.Context, db backend.DBTX, appID string) error {
	return pgsqlgen.New(db).DeleteAppDomainsByApp(ctx, appID)
}

func (pgsqlQuerier) DeleteAppDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) error {
	return pgsqlgen.New(db).DeleteAppDomainsByServer(ctx, serverID)
}

func (pgsqlQuerier) ListAppDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]domainRow, error) {
	rows, err := pgsqlgen.New(db).ListAppDomainsByServer(ctx, serverID)
	if err != nil {
		return nil, err
	}
	out := make([]domainRow, len(rows))
	for i, row := range rows {
		out[i] = domainRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListAppDomainNames(ctx context.Context, db backend.DBTX, p namesParams) ([]string, error) {
	return pgsqlgen.New(db).ListAppDomainNames(ctx, pgsqlgen.ListAppDomainNamesParams(p))
}
