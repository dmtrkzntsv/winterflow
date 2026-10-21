package port

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
)

// HubRegistry is the list of live hubs: each hub announces itself with a
// time to live and drops out of the list when it stops renewing.
type HubRegistry interface {
	// Announce writes the hub's entry, live for ttl.
	Announce(ctx context.Context, hub model.Hub, ttl time.Duration) error
	// Withdraw removes the hub's entry at once (a hub shutting down).
	Withdraw(ctx context.Context, hubID string) error
	// List returns every live hub.
	List(ctx context.Context) ([]model.Hub, error)
}
