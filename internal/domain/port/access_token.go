package port

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
)

// AccessTokenRepository stores access tokens (sessions, PATs, OAuth access
// tokens) by the SHA-256 of their raw value. Liveness (expired, revoked) is
// judged by the caller; reads return rows as stored.
type AccessTokenRepository interface {
	// Insert stores t under hash.
	Insert(ctx context.Context, t model.AccessToken, hash string) error
	// GetByHash returns the token with that hash; model.ErrNotFound when
	// none.
	GetByHash(ctx context.Context, hash string) (model.AccessToken, error)
	// Touch stamps last_used_at and sets expires_at on an unrevoked token;
	// false when the token is gone or revoked.
	Touch(ctx context.Context, id string, lastUsedAt time.Time, expiresAt *time.Time) (bool, error)
	// Revoke revokes one token (a no-op when already revoked or gone).
	Revoke(ctx context.Context, id string, now time.Time) error
	// RevokeUserToken revokes the user's live token of kind id; false when
	// there is none (unknown, another user's, another kind, already
	// revoked).
	RevokeUserToken(ctx context.Context, userID string, kind model.TokenKind, id string, now time.Time) (bool, error)
	// RevokeAllOfKind revokes every live token of kind the user has.
	RevokeAllOfKind(ctx context.Context, userID string, kind model.TokenKind, now time.Time) error
	// RevokeByGrant revokes every live token issued for an OAuth grant.
	RevokeByGrant(ctx context.Context, grantID string, now time.Time) error
	// ListByUser returns the user's tokens of kind, revoked ones included,
	// oldest first.
	ListByUser(ctx context.Context, userID string, kind model.TokenKind) ([]model.AccessToken, error)
	// DeleteDead deletes tokens revoked or expired before cutoff.
	DeleteDead(ctx context.Context, cutoff time.Time) (int64, error)
}

// GrantRevoker revokes a user's OAuth grants (the authorization server's,
// wired once it exists), so their refresh tokens stop working too.
type GrantRevoker interface {
	RevokeUserGrants(ctx context.Context, userID string) error
}
