// Package accesstoken implements port.AccessTokenRepository: login sessions,
// personal access tokens and OAuth access tokens in one table, stored by the
// SHA-256 of the raw token.
package accesstoken

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/util"
)

type (
	tokenRow          = sqlitegen.AccessToken
	insertParams      = sqlitegen.InsertAccessTokenParams
	touchParams       = sqlitegen.TouchAccessTokenParams
	revokeParams      = sqlitegen.RevokeAccessTokenParams
	revokeUserParams  = sqlitegen.RevokeUserAccessTokenParams
	revokeKindParams  = sqlitegen.RevokeUserAccessTokensParams
	revokeGrantParams = sqlitegen.RevokeAccessTokensByGrantParams
	listParams        = sqlitegen.ListAccessTokensByUserParams
	deleteDeadParams  = sqlitegen.DeleteDeadAccessTokensParams
)

type querier interface {
	InsertAccessToken(ctx context.Context, db backend.DBTX, p insertParams) error
	GetAccessTokenByHash(ctx context.Context, db backend.DBTX, hash string) (tokenRow, error)
	TouchAccessToken(ctx context.Context, db backend.DBTX, p touchParams) (int64, error)
	RevokeAccessToken(ctx context.Context, db backend.DBTX, p revokeParams) error
	RevokeUserAccessToken(ctx context.Context, db backend.DBTX, p revokeUserParams) (int64, error)
	RevokeUserAccessTokens(ctx context.Context, db backend.DBTX, p revokeKindParams) error
	RevokeAccessTokensByGrant(ctx context.Context, db backend.DBTX, p revokeGrantParams) error
	ListAccessTokensByUser(ctx context.Context, db backend.DBTX, p listParams) ([]tokenRow, error)
	DeleteDeadAccessTokens(ctx context.Context, db backend.DBTX, p deleteDeadParams) (int64, error)
}

// Repo is the access tokens repository.
type Repo struct {
	tx *backend.TxManager
	q  querier
}

var _ port.AccessTokenRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}}, nil
	}
	return nil, fmt.Errorf("access token repository: unknown database driver %q", driver)
}

// Insert stores t under hash. Empty optional strings are stored as NULL, a
// zero LastUsedAt as never used.
func (r *Repo) Insert(ctx context.Context, t model.AccessToken, hash string) error {
	return r.q.InsertAccessToken(ctx, r.tx.Querier(ctx), insertParams{
		ID:          t.ID,
		UserID:      t.UserID,
		Kind:        string(t.Kind),
		TokenHash:   hash,
		Scope:       t.Scope,
		Name:        util.RefString(t.Name),
		TokenPrefix: util.RefString(t.Prefix),
		UserAgent:   util.RefString(t.UserAgent),
		GrantID:     util.RefString(t.GrantID),
		CreatedAt:   types.DateTime(t.CreatedAt),
		LastUsedAt:  optTime(t.LastUsedAt),
		ExpiresAt:   ptrTime(t.ExpiresAt),
	})
}

// GetByHash returns the token stored under hash; model.ErrNotFound when none.
func (r *Repo) GetByHash(ctx context.Context, hash string) (model.AccessToken, error) {
	row, err := r.q.GetAccessTokenByHash(ctx, r.tx.Querier(ctx), hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.AccessToken{}, model.ErrNotFound
		}
		return model.AccessToken{}, err
	}
	return hydrate(row), nil
}

// Touch stamps last_used_at and expires_at of an unrevoked token; false when
// it is revoked or gone.
func (r *Repo) Touch(ctx context.Context, id string, lastUsedAt time.Time, expiresAt *time.Time) (bool, error) {
	n, err := r.q.TouchAccessToken(ctx, r.tx.Querier(ctx), touchParams{
		LastUsedAt: optTime(lastUsedAt), ExpiresAt: ptrTime(expiresAt), ID: id,
	})
	return n == 1, err
}

// Revoke revokes one token.
func (r *Repo) Revoke(ctx context.Context, id string, now time.Time) error {
	return r.q.RevokeAccessToken(ctx, r.tx.Querier(ctx), revokeParams{RevokedAt: optTime(now), ID: id})
}

// RevokeUserToken revokes the user's live token of kind id; false when
// there is no such token.
func (r *Repo) RevokeUserToken(ctx context.Context, userID string, kind model.TokenKind, id string, now time.Time) (bool, error) {
	n, err := r.q.RevokeUserAccessToken(ctx, r.tx.Querier(ctx), revokeUserParams{
		RevokedAt: optTime(now), ID: id, UserID: userID, Kind: string(kind),
	})
	return n == 1, err
}

// RevokeAllOfKind revokes every live token of kind the user has, in one
// statement.
func (r *Repo) RevokeAllOfKind(ctx context.Context, userID string, kind model.TokenKind, now time.Time) error {
	return r.q.RevokeUserAccessTokens(ctx, r.tx.Querier(ctx), revokeKindParams{
		RevokedAt: optTime(now), UserID: userID, Kind: string(kind),
	})
}

// RevokeByGrant revokes every live token issued for the grant.
func (r *Repo) RevokeByGrant(ctx context.Context, grantID string, now time.Time) error {
	return r.q.RevokeAccessTokensByGrant(ctx, r.tx.Querier(ctx), revokeGrantParams{
		RevokedAt: optTime(now), GrantID: &grantID,
	})
}

// ListByUser returns the user's tokens of kind, oldest first.
func (r *Repo) ListByUser(ctx context.Context, userID string, kind model.TokenKind) ([]model.AccessToken, error) {
	rows, err := r.q.ListAccessTokensByUser(ctx, r.tx.Querier(ctx), listParams{UserID: userID, Kind: string(kind)})
	if err != nil {
		return nil, err
	}
	out := make([]model.AccessToken, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrate(row))
	}
	return out, nil
}

// DeleteDead deletes tokens revoked or expired before cutoff.
func (r *Repo) DeleteDead(ctx context.Context, cutoff time.Time) (int64, error) {
	c := types.DateTime(cutoff)
	return r.q.DeleteDeadAccessTokens(ctx, r.tx.Querier(ctx), deleteDeadParams{RevokedAt: &c, ExpiresAt: &c})
}

func hydrate(row tokenRow) model.AccessToken {
	t := model.AccessToken{
		ID:        row.ID,
		UserID:    row.UserID,
		Kind:      model.TokenKind(row.Kind),
		Scope:     row.Scope,
		Name:      util.DerefString(row.Name),
		Prefix:    util.DerefString(row.TokenPrefix),
		UserAgent: util.DerefString(row.UserAgent),
		GrantID:   util.DerefString(row.GrantID),
		CreatedAt: row.CreatedAt.Time(),
		ExpiresAt: timePtr(row.ExpiresAt),
		RevokedAt: timePtr(row.RevokedAt),
	}
	if row.LastUsedAt != nil {
		t.LastUsedAt = row.LastUsedAt.Time()
	}
	return t
}

// optTime is nil for the zero time.
func optTime(t time.Time) *types.DateTime {
	if t.IsZero() {
		return nil
	}
	dt := types.DateTime(t)
	return &dt
}

func ptrTime(t *time.Time) *types.DateTime {
	if t == nil {
		return nil
	}
	return optTime(*t)
}

func timePtr(dt *types.DateTime) *time.Time {
	if dt == nil {
		return nil
	}
	t := dt.Time()
	return &t
}
