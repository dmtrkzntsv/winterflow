// Package hubtoken implements port.HubTokenRepository: one-time hub join
// tokens, stored hashed.
package hubtoken

import (
	"context"
	"fmt"
	"time"

	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
)

type (
	insertParams  = sqlitegen.InsertHubJoinTokenParams
	consumeParams = sqlitegen.ConsumeHubJoinTokenParams
	pruneParams   = sqlitegen.PruneHubJoinTokensParams
)

type querier interface {
	InsertHubJoinToken(ctx context.Context, db backend.DBTX, p insertParams) error
	ConsumeHubJoinToken(ctx context.Context, db backend.DBTX, p consumeParams) (int64, error)
	PruneHubJoinTokens(ctx context.Context, db backend.DBTX, p pruneParams) error
}

// Repo is the hub join tokens repository.
type Repo struct {
	tx *backend.TxManager
	q  querier
}

var _ port.HubTokenRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}}, nil
	}
	return nil, fmt.Errorf("hub token repository: unknown database driver %q", driver)
}

// Create stores a token hash that expires at expiresAt.
func (r *Repo) Create(ctx context.Context, hash string, expiresAt, now time.Time) error {
	return r.q.InsertHubJoinToken(ctx, r.tx.Querier(ctx), insertParams{
		TokenHash: hash, ExpiresAt: types.DateTime(expiresAt), CreatedAt: types.DateTime(now),
	})
}

// Consume is one conditional UPDATE: of any number of concurrent callers
// exactly one sees a row change.
func (r *Repo) Consume(ctx context.Context, hash, hubID string, now time.Time) (bool, error) {
	used := types.DateTime(now)
	n, err := r.q.ConsumeHubJoinToken(ctx, r.tx.Querier(ctx), consumeParams{
		UsedAt: &used, UsedByHub: &hubID, TokenHash: hash, ExpiresAt: types.DateTime(now),
	})
	return n == 1, err
}

// Prune deletes tokens whose expiry (unused) or use is before cutoff.
func (r *Repo) Prune(ctx context.Context, cutoff time.Time) error {
	c := types.DateTime(cutoff)
	return r.q.PruneHubJoinTokens(ctx, r.tx.Querier(ctx), pruneParams{ExpiresAt: c, UsedAt: &c})
}
