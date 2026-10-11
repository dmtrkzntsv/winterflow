// Package serverdomain implements port.ServerDomainRepository: the API's
// index of each agent's domain registrations (server_domains). The agent's
// domains/ dir stays authoritative; every method here is cache maintenance.
package serverdomain

import (
	"context"
	"fmt"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

// Row and parameter types are SQLite's; pgsql.go converts Postgres's
// identically shaped ones.
type (
	domainRow    = sqlitegen.ServerDomain
	upsertParams = sqlitegen.UpsertServerDomainParams
	insertParams = sqlitegen.InsertServerDomainParams
	deleteParams = sqlitegen.DeleteServerDomainParams
)

type querier interface {
	ListServerDomains(ctx context.Context, db backend.DBTX, serverID string) ([]domainRow, error)
	UpsertServerDomain(ctx context.Context, db backend.DBTX, p upsertParams) error
	InsertServerDomain(ctx context.Context, db backend.DBTX, p insertParams) error
	DeleteServerDomain(ctx context.Context, db backend.DBTX, p deleteParams) error
	DeleteServerDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) error
}

// Repo is the server_domains repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.ServerDomainRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("serverdomain repository: unknown database driver %q", driver)
}

// ListForServer returns the server's registrations sorted by name.
func (r *Repo) ListForServer(ctx context.Context, serverID string) ([]model.RegisteredDomain, error) {
	rows, err := r.q.ListServerDomains(ctx, r.tx.Querier(ctx), serverID)
	if err != nil {
		return nil, err
	}
	out := make([]model.RegisteredDomain, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrate(row))
	}
	return out, nil
}

// Upsert records one registration (domain.create confirmed by the agent).
func (r *Repo) Upsert(ctx context.Context, serverID string, d model.RegisteredDomain) error {
	return r.q.UpsertServerDomain(ctx, r.tx.Querier(ctx), upsertParams(rowFor(serverID, d)))
}

// Delete drops one registration (domain.delete confirmed by the agent).
func (r *Repo) Delete(ctx context.Context, serverID, name string) error {
	return r.q.DeleteServerDomain(ctx, r.tx.Querier(ctx), deleteParams{ServerID: serverID, Name: name})
}

// ReplaceForServer makes the index mirror a domain.list answer. An answer
// equal to the stored rows (the common case: every agent reconnect sends
// one) writes nothing.
func (r *Repo) ReplaceForServer(ctx context.Context, serverID string, ds []model.RegisteredDomain) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		current, err := r.ListForServer(ctx, serverID)
		if err != nil {
			return err
		}
		if sameDomains(current, ds) {
			return nil
		}
		if err := r.q.DeleteServerDomainsByServer(ctx, r.tx.Querier(ctx), serverID); err != nil {
			return err
		}
		for _, d := range ds {
			if err := r.q.InsertServerDomain(ctx, r.tx.Querier(ctx), rowFor(serverID, d)); err != nil {
				return err
			}
		}
		return nil
	})
}

// rowFor is the row a registration is stored as: expiry in UTC, updated now.
func rowFor(serverID string, d model.RegisteredDomain) insertParams {
	p := insertParams{
		ServerID:  serverID,
		Name:      d.Name,
		Tls:       string(d.TLS),
		Issuer:    d.Issuer,
		UpdatedAt: types.DateTime(time.Now().UTC()),
	}
	if d.NotAfter != nil {
		na := types.DateTime(d.NotAfter.UTC())
		p.NotAfter = &na
	}
	return p
}

func hydrate(row domainRow) model.RegisteredDomain {
	d := model.RegisteredDomain{Name: row.Name, TLS: model.DomainTLS(row.Tls), Issuer: row.Issuer}
	if row.NotAfter != nil {
		na := row.NotAfter.Time().UTC()
		d.NotAfter = &na
	}
	return d
}

// sameDomains reports whether two registration lists hold the same rows,
// in any order (expiries compared to the second, as stored).
func sameDomains(a, b []model.RegisteredDomain) bool {
	if len(a) != len(b) {
		return false
	}
	byName := make(map[string]model.RegisteredDomain, len(a))
	for _, d := range a {
		byName[d.Name] = d
	}
	for _, d := range b {
		o, ok := byName[d.Name]
		if !ok || o.TLS != d.TLS || o.Issuer != d.Issuer || (o.NotAfter == nil) != (d.NotAfter == nil) ||
			(o.NotAfter != nil && !o.NotAfter.Truncate(time.Second).Equal(d.NotAfter.Truncate(time.Second))) {
			return false
		}
	}
	return true
}
