package port

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
)

// SecretRequestRepository stores secret links (secret_requests).
type SecretRequestRepository interface {
	// Create inserts a pending request stored under tokenHash.
	Create(ctx context.Context, r model.SecretRequest, tokenHash string) error
	// GetByTokenHash reads the request of a link token's hash
	// (model.ErrNotFound for none).
	GetByTokenHash(ctx context.Context, tokenHash string) (model.SecretRequest, error)
	// Get reads the org's request; another org's, or none, is
	// model.ErrNotFound.
	Get(ctx context.Context, orgID, id string) (model.SecretRequest, error)
	// Submit stores the ciphertexts of a pending request whose deadline is
	// after now, moving its deadline to expiresAt; reports whether it did.
	Submit(ctx context.Context, id string, ciphertexts map[string]string, now, expiresAt time.Time) (bool, error)
	// Claim takes a submitted request (or one whose claim lapsed) whose
	// deadline is after now for claimID until claimedUntil; reports
	// whether it did.
	Claim(ctx context.Context, id, claimID string, now, claimedUntil time.Time) (bool, error)
	// MarkDispatched marks a request claimed by claimID dispatched (right
	// before its save is sent) until claimedUntil: no Claim takes it again;
	// false when the request is no longer claimed by claimID.
	MarkDispatched(ctx context.Context, id, claimID string, claimedUntil time.Time) (bool, error)
	// MarkUsed ends a request claimID dispatched as used and deletes its
	// ciphertexts; reports whether it did.
	MarkUsed(ctx context.Context, id, claimID string, now time.Time) (bool, error)
	// Release returns a request claimed (or dispatched, when the send
	// failed) by claimID to submitted; reports whether it did.
	Release(ctx context.Context, id, claimID string) (bool, error)
	// Expire ends every pending or submitted request (or claimed one whose
	// claim lapsed) past its deadline, deleting its ciphertexts, and ends
	// as used every dispatched one past its claim deadline (its save went
	// away before marking it used).
	Expire(ctx context.Context, now time.Time) (int64, error)
	// Prune deletes requests that ended before cutoff.
	Prune(ctx context.Context, cutoff time.Time) (int64, error)
}
