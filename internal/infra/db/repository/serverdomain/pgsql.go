package serverdomain

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) ListServerDomains(ctx context.Context, db backend.DBTX, serverID string) ([]domainRow, error) {
	rows, err := pgsqlgen.New(db).ListServerDomains(ctx, serverID)
	if err != nil {
		return nil, err
	}
	out := make([]domainRow, len(rows))
	for i, row := range rows {
		out[i] = domainRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) UpsertServerDomain(ctx context.Context, db backend.DBTX, p upsertParams) error {
	return pgsqlgen.New(db).UpsertServerDomain(ctx, pgsqlgen.UpsertServerDomainParams(p))
}

func (pgsqlQuerier) InsertServerDomain(ctx context.Context, db backend.DBTX, p insertParams) error {
	return pgsqlgen.New(db).InsertServerDomain(ctx, pgsqlgen.InsertServerDomainParams(p))
}

func (pgsqlQuerier) DeleteServerDomain(ctx context.Context, db backend.DBTX, p deleteParams) error {
	return pgsqlgen.New(db).DeleteServerDomain(ctx, pgsqlgen.DeleteServerDomainParams(p))
}

func (pgsqlQuerier) DeleteServerDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) error {
	return pgsqlgen.New(db).DeleteServerDomainsByServer(ctx, serverID)
}
