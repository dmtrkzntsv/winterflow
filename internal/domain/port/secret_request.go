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
	// MarkUsed ends a submitted request as used and deletes its
	// ciphertexts; reports whether it did.
	MarkUsed(ctx context.Context, id string, now time.Time) (bool, error)
	// Expire ends every pending or submitted request past its deadline,
	// deleting its ciphertexts.
	Expire(ctx context.Context, now time.Time) (int64, error)
	// Prune deletes requests that ended before cutoff.
	Prune(ctx context.Context, cutoff time.Time) (int64, error)
}
