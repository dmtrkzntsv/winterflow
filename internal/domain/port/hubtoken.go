package port

import (
	"context"
	"time"
)

// HubTokenRepository stores the one-time tokens that let a new hub fetch its
// certificate. Only token hashes are stored.
type HubTokenRepository interface {
	Create(ctx context.Context, hash string, expiresAt, now time.Time) error
	// Consume marks the token used by hubID in one conditional update and
	// reports whether it did: false for an unknown, used or expired token.
	Consume(ctx context.Context, hash, hubID string, now time.Time) (bool, error)
	// Prune deletes tokens expired or used before cutoff.
	Prune(ctx context.Context, cutoff time.Time) error
}
