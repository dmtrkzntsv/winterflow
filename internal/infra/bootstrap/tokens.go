package bootstrap

import (
	"context"
	"time"

	"winterflow/pkg/logger"
)

const (
	// tokenPruneInterval is how often dead access tokens are deleted.
	tokenPruneInterval = time.Hour
	// tokenPruneTimeout bounds one prune.
	tokenPruneTimeout = time.Minute
)

// tokenPruner is the slice of the authn usecase the loop needs.
type tokenPruner interface {
	Prune(ctx context.Context) error
}

// tokenPruneTick deletes access tokens revoked or expired more than 30 days
// ago.
func tokenPruneTick(ctx context.Context, tokens tokenPruner, log *logger.Logger) {
	ctx, cancel := context.WithTimeout(ctx, tokenPruneTimeout)
	defer cancel()
	if err := tokens.Prune(ctx); err != nil {
		log.Warn("access tokens: prune failed", "error", err)
	}
}

// startTokenMaintenance prunes dead access tokens every hour on its own
// goroutine until ctx is done.
func startTokenMaintenance(ctx context.Context, tokens tokenPruner, log *logger.Logger) {
	go func() {
		ticker := time.NewTicker(tokenPruneInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tokenPruneTick(ctx, tokens, log)
			}
		}
	}()
}
