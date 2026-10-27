// Package authserver implements port.AuthServerRepository: the OAuth
// authorization server's registered clients, one-shot codes, grants and
// spent refresh-token hashes.
package authserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
)

type (
	clientRow         = sqlitegen.OauthClient
	codeRow           = sqlitegen.OauthCode
	grantRow          = sqlitegen.OauthGrant
	listGrantRow      = sqlitegen.ListUnrevokedOAuthGrantsRow
	insertClientParam = sqlitegen.InsertOAuthClientParams
	markClientParams  = sqlitegen.MarkOAuthClientUsedParams
	insertCodeParams  = sqlitegen.InsertOAuthCodeParams
	insertGrantParams = sqlitegen.InsertOAuthGrantParams
	rotateParams      = sqlitegen.RotateOAuthGrantParams
	revokeParams      = sqlitegen.RevokeOAuthGrantParams
	revokeUserParams  = sqlitegen.RevokeUserOAuthGrantsParams
	revokeOtherParams = sqlitegen.RevokeOtherOAuthGrantsParams
	deleteDeadParams  = sqlitegen.DeleteDeadOAuthGrantsParams
	insertSpentParams = sqlitegen.InsertOAuthSpentRefreshHashParams
)

type querier interface {
	InsertOAuthClient(ctx context.Context, db backend.DBTX, p insertClientParam) error
	GetOAuthClient(ctx context.Context, db backend.DBTX, id string) (clientRow, error)
	MarkOAuthClientUsed(ctx context.Context, db backend.DBTX, p markClientParams) error
	PurgeUnusedOAuthClients(ctx context.Context, db backend.DBTX, createdBefore types.DateTime) (int64, error)
	CountUnusedOAuthClients(ctx context.Context, db backend.DBTX) (int64, error)

	InsertOAuthCode(ctx context.Context, db backend.DBTX, p insertCodeParams) error
	ConsumeOAuthCode(ctx context.Context, db backend.DBTX, codeHash string) (codeRow, error)
	PurgeExpiredOAuthCodes(ctx context.Context, db backend.DBTX, before types.DateTime) error

	InsertOAuthGrant(ctx context.Context, db backend.DBTX, p insertGrantParams) error
	GetOAuthGrant(ctx context.Context, db backend.DBTX, id string) (grantRow, error)
	GetOAuthGrantByRefreshHash(ctx context.Context, db backend.DBTX, hash string) (grantRow, error)
	GetOAuthGrantBySpentRefreshHash(ctx context.Context, db backend.DBTX, hash string) (grantRow, error)
	InsertOAuthSpentRefreshHash(ctx context.Context, db backend.DBTX, p insertSpentParams) error
	RotateOAuthGrant(ctx context.Context, db backend.DBTX, p rotateParams) (int64, error)
	RevokeOAuthGrant(ctx context.Context, db backend.DBTX, p revokeParams) (int64, error)
	RevokeUserOAuthGrants(ctx context.Context, db backend.DBTX, p revokeUserParams) (int64, error)
	RevokeOtherOAuthGrants(ctx context.Context, db backend.DBTX, p revokeOtherParams) ([]string, error)
	ListUnrevokedOAuthGrants(ctx context.Context, db backend.DBTX, userID string) ([]listGrantRow, error)
	DeleteDeadOAuthGrants(ctx context.Context, db backend.DBTX, p deleteDeadParams) (int64, error)
}

// Repo is the authorization server repository.
type Repo struct {
	tx *backend.TxManager
	q  querier
}

var _ port.AuthServerRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}}, nil
	}
	return nil, fmt.Errorf("authorization server repository: unknown database driver %q", driver)
}

func (r *Repo) db(ctx context.Context) backend.DBTX { return r.tx.Querier(ctx) }

// InsertClient stores a registered client; its redirect URIs as a JSON
// array.
func (r *Repo) InsertClient(ctx context.Context, c model.OAuthClient) error {
	uris, err := json.Marshal(c.RedirectURIs)
	if err != nil {
		return err
	}
	return r.q.InsertOAuthClient(ctx, r.db(ctx), insertClientParam{
		ID: c.ID, Name: c.Name, RedirectUris: string(uris), SecretHash: c.SecretHash,
		CreatedAt: types.DateTime(c.CreatedAt), LastUsedAt: ptrTime(c.LastUsedAt),
	})
}

// GetClient returns the client; model.ErrNotFound when absent.
func (r *Repo) GetClient(ctx context.Context, id string) (model.OAuthClient, error) {
	row, err := r.q.GetOAuthClient(ctx, r.db(ctx), id)
	if err != nil {
		return model.OAuthClient{}, notFound(err)
	}
	uris, err := parseURIs(row.RedirectUris)
	if err != nil {
		return model.OAuthClient{}, err
	}
	return model.OAuthClient{ID: row.ID, Name: row.Name, RedirectURIs: uris, SecretHash: row.SecretHash,
		CreatedAt: row.CreatedAt.Time(), LastUsedAt: timePtr(row.LastUsedAt)}, nil
}

// MarkClientUsed stamps the client's last approval.
func (r *Repo) MarkClientUsed(ctx context.Context, id string, now time.Time) error {
	return r.q.MarkOAuthClientUsed(ctx, r.db(ctx), markClientParams{LastUsedAt: ptrTime(&now), ID: id})
}

// PurgeUnusedClients deletes clients never approved, registered before
// createdBefore.
func (r *Repo) PurgeUnusedClients(ctx context.Context, createdBefore time.Time) (int64, error) {
	return r.q.PurgeUnusedOAuthClients(ctx, r.db(ctx), types.DateTime(createdBefore))
}

// CountUnusedClients counts clients never approved.
func (r *Repo) CountUnusedClients(ctx context.Context) (int64, error) {
	return r.q.CountUnusedOAuthClients(ctx, r.db(ctx))
}

// InsertCode stores an authorization code by its hash.
func (r *Repo) InsertCode(ctx context.Context, c model.OAuthCode) error {
	return r.q.InsertOAuthCode(ctx, r.db(ctx), insertCodeParams{
		CodeHash: c.CodeHash, ClientID: c.ClientID, UserID: c.UserID, RedirectUri: c.RedirectURI,
		CodeChallenge: c.CodeChallenge, Resource: c.Resource,
		CreatedAt: types.DateTime(c.CreatedAt), ExpiresAt: types.DateTime(c.ExpiresAt),
	})
}

// ConsumeCode deletes and returns the code; model.ErrNotFound when absent.
func (r *Repo) ConsumeCode(ctx context.Context, codeHash string) (model.OAuthCode, error) {
	row, err := r.q.ConsumeOAuthCode(ctx, r.db(ctx), codeHash)
	if err != nil {
		return model.OAuthCode{}, notFound(err)
	}
	return model.OAuthCode{
		CodeHash: row.CodeHash, ClientID: row.ClientID, UserID: row.UserID, RedirectURI: row.RedirectUri,
		CodeChallenge: row.CodeChallenge, Resource: row.Resource,
		CreatedAt: row.CreatedAt.Time(), ExpiresAt: row.ExpiresAt.Time(),
	}, nil
}

// PurgeExpiredCodes deletes codes expired before before.
func (r *Repo) PurgeExpiredCodes(ctx context.Context, before time.Time) error {
	return r.q.PurgeExpiredOAuthCodes(ctx, r.db(ctx), types.DateTime(before))
}

// InsertGrant stores a grant.
func (r *Repo) InsertGrant(ctx context.Context, g model.OAuthGrant) error {
	return r.q.InsertOAuthGrant(ctx, r.db(ctx), insertGrantParams{
		ID: g.ID, UserID: g.UserID, ClientID: g.ClientID,
		RefreshTokenHash: g.RefreshTokenHash, PrevRefreshTokenHash: g.PrevRefreshTokenHash, RotatedAt: ptrTime(g.RotatedAt),
		CreatedAt: types.DateTime(g.CreatedAt), LastUsedAt: types.DateTime(g.LastUsedAt),
		ExpiresAt: types.DateTime(g.ExpiresAt), RevokedAt: ptrTime(g.RevokedAt),
	})
}

// GetGrant returns the grant; model.ErrNotFound when absent.
func (r *Repo) GetGrant(ctx context.Context, id string) (model.OAuthGrant, error) {
	row, err := r.q.GetOAuthGrant(ctx, r.db(ctx), id)
	return grantResult(row, err)
}

// GetGrantByRefreshHash returns the grant whose current refresh token has
// the hash; model.ErrNotFound when none.
func (r *Repo) GetGrantByRefreshHash(ctx context.Context, hash string) (model.OAuthGrant, error) {
	row, err := r.q.GetOAuthGrantByRefreshHash(ctx, r.db(ctx), hash)
	return grantResult(row, err)
}

// GetGrantBySpentRefreshHash returns the grant that rotated the token
// away; model.ErrNotFound when it was never spent.
func (r *Repo) GetGrantBySpentRefreshHash(ctx context.Context, hash string) (model.OAuthGrant, error) {
	row, err := r.q.GetOAuthGrantBySpentRefreshHash(ctx, r.db(ctx), hash)
	return grantResult(row, err)
}

// InsertSpentRefreshHash remembers a refresh token rotated away.
func (r *Repo) InsertSpentRefreshHash(ctx context.Context, grantID, hash string, now time.Time) error {
	return r.q.InsertOAuthSpentRefreshHash(ctx, r.db(ctx), insertSpentParams{
		TokenHash: hash, GrantID: grantID, SpentAt: types.DateTime(now),
	})
}

// RotateGrant swaps the refresh hash, conditional on oldHash being current
// and the grant unrevoked.
func (r *Repo) RotateGrant(ctx context.Context, id, oldHash, newHash string, now, expiresAt time.Time) (int64, error) {
	return r.q.RotateOAuthGrant(ctx, r.db(ctx), rotateParams{
		RefreshTokenHash: newHash, RotatedAt: ptrTime(&now), LastUsedAt: types.DateTime(now),
		ExpiresAt: types.DateTime(expiresAt), ID: id, RefreshTokenHash_2: oldHash,
	})
}

// RevokeGrant revokes one unrevoked grant.
func (r *Repo) RevokeGrant(ctx context.Context, id string, now time.Time) (int64, error) {
	return r.q.RevokeOAuthGrant(ctx, r.db(ctx), revokeParams{RevokedAt: ptrTime(&now), ID: id})
}

// RevokeUserGrants revokes every unrevoked grant of the user.
func (r *Repo) RevokeUserGrants(ctx context.Context, userID string, now time.Time) (int64, error) {
	return r.q.RevokeUserOAuthGrants(ctx, r.db(ctx), revokeUserParams{RevokedAt: ptrTime(&now), UserID: userID})
}

// RevokeOtherGrants revokes the user's other unrevoked grants for the
// client, returning their ids.
func (r *Repo) RevokeOtherGrants(ctx context.Context, userID, clientID, keepID string, now time.Time) ([]string, error) {
	return r.q.RevokeOtherOAuthGrants(ctx, r.db(ctx), revokeOtherParams{
		RevokedAt: ptrTime(&now), UserID: userID, ClientID: clientID, ID: keepID,
	})
}

// ListUnrevokedGrants lists the user's unrevoked grants with their clients.
func (r *Repo) ListUnrevokedGrants(ctx context.Context, userID string) ([]model.ConnectedGrant, error) {
	rows, err := r.q.ListUnrevokedOAuthGrants(ctx, r.db(ctx), userID)
	if err != nil {
		return nil, err
	}
	out := make([]model.ConnectedGrant, 0, len(rows))
	for _, row := range rows {
		uris, err := parseURIs(row.RedirectUris)
		if err != nil {
			return nil, err
		}
		g := hydrateGrant(grantRow{
			ID: row.ID, UserID: row.UserID, ClientID: row.ClientID, RefreshTokenHash: row.RefreshTokenHash,
			PrevRefreshTokenHash: row.PrevRefreshTokenHash, RotatedAt: row.RotatedAt, CreatedAt: row.CreatedAt,
			LastUsedAt: row.LastUsedAt, ExpiresAt: row.ExpiresAt, RevokedAt: row.RevokedAt,
		})
		out = append(out, model.ConnectedGrant{Grant: g, ClientName: row.Name, RedirectURIs: uris})
	}
	return out, nil
}

// DeleteDeadGrants deletes grants revoked or expired before cutoff (their
// spent hashes go with them).
func (r *Repo) DeleteDeadGrants(ctx context.Context, cutoff time.Time) (int64, error) {
	return r.q.DeleteDeadOAuthGrants(ctx, r.db(ctx), deleteDeadParams{RevokedAt: ptrTime(&cutoff), ExpiresAt: types.DateTime(cutoff)})
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return model.ErrNotFound
	}
	return err
}

func grantResult(row grantRow, err error) (model.OAuthGrant, error) {
	if err != nil {
		return model.OAuthGrant{}, notFound(err)
	}
	return hydrateGrant(row), nil
}

func hydrateGrant(row grantRow) model.OAuthGrant {
	return model.OAuthGrant{
		ID: row.ID, UserID: row.UserID, ClientID: row.ClientID,
		RefreshTokenHash: row.RefreshTokenHash, PrevRefreshTokenHash: row.PrevRefreshTokenHash,
		RotatedAt: timePtr(row.RotatedAt), CreatedAt: row.CreatedAt.Time(), LastUsedAt: row.LastUsedAt.Time(),
		ExpiresAt: row.ExpiresAt.Time(), RevokedAt: timePtr(row.RevokedAt),
	}
}

func parseURIs(raw string) ([]string, error) {
	var uris []string
	if err := json.Unmarshal([]byte(raw), &uris); err != nil {
		return nil, fmt.Errorf("oauth client redirect_uris: %w", err)
	}
	return uris, nil
}

func ptrTime(t *time.Time) *types.DateTime {
	if t == nil {
		return nil
	}
	dt := types.DateTime(*t)
	return &dt
}

func timePtr(dt *types.DateTime) *time.Time {
	if dt == nil {
		return nil
	}
	t := dt.Time()
	return &t
}
