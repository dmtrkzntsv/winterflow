package port

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
)

// AuthServerRepository stores the OAuth authorization server's clients,
// codes and grants. Gets answer model.ErrNotFound when the row is absent.
type AuthServerRepository interface {
	InsertClient(ctx context.Context, c model.OAuthClient) error
	GetClient(ctx context.Context, id string) (model.OAuthClient, error)
	MarkClientUsed(ctx context.Context, id string, now time.Time) error
	// PurgeUnusedClients deletes clients never approved and registered
	// before createdBefore.
	PurgeUnusedClients(ctx context.Context, createdBefore time.Time) (int64, error)
	// CountUnusedClients counts clients never approved.
	CountUnusedClients(ctx context.Context) (int64, error)

	InsertCode(ctx context.Context, c model.OAuthCode) error
	// ConsumeCode deletes the code and returns it (the delete is the read,
	// so a code is used at most once).
	ConsumeCode(ctx context.Context, codeHash string) (model.OAuthCode, error)
	PurgeExpiredCodes(ctx context.Context, before time.Time) error

	InsertGrant(ctx context.Context, g model.OAuthGrant) error
	GetGrant(ctx context.Context, id string) (model.OAuthGrant, error)
	GetGrantByRefreshHash(ctx context.Context, hash string) (model.OAuthGrant, error)
	// GetGrantBySpentRefreshHash finds the grant that rotated the token
	// away, however many rotations ago.
	GetGrantBySpentRefreshHash(ctx context.Context, hash string) (model.OAuthGrant, error)
	InsertSpentRefreshHash(ctx context.Context, grantID, hash string, now time.Time) error
	// RotateGrant replaces the refresh hash oldHash with newHash on an
	// unrevoked grant; 0 rows when oldHash is no longer current.
	RotateGrant(ctx context.Context, id, oldHash, newHash string, now, expiresAt time.Time) (int64, error)
	RevokeGrant(ctx context.Context, id string, now time.Time) (int64, error)
	RevokeUserGrants(ctx context.Context, userID string, now time.Time) (int64, error)
	// RevokeOtherGrants revokes the user's unrevoked grants for the client
	// except keepID, returning the ids it revoked.
	RevokeOtherGrants(ctx context.Context, userID, clientID, keepID string, now time.Time) ([]string, error)
	// LockUserGrants serializes grant changes for the user until the
	// surrounding transaction ends (model.ErrNotFound when the user is
	// gone): taken before a code exchange inserts its grant, so two
	// concurrent exchanges cannot both miss each other's grant.
	LockUserGrants(ctx context.Context, userID string) error
	// ListUnrevokedGrants lists the user's unrevoked grants with their
	// clients, oldest first.
	ListUnrevokedGrants(ctx context.Context, userID string) ([]model.ConnectedGrant, error)
	// DeleteDeadGrants deletes grants revoked or expired before cutoff.
	DeleteDeadGrants(ctx context.Context, cutoff time.Time) (int64, error)
}
