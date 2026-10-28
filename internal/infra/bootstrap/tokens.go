package bootstrap

import (
	"context"
	"time"

	"winterflow/pkg/logger"
)

const (
	// tokenPruneFirst is when the first prune runs after start: soon, so a
	// process restarted more often than tokenPruneInterval still prunes.
	tokenPruneFirst = time.Minute
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

// startTokenMaintenance prunes dead access tokens a minute after start, then
// every hour, on its own goroutine until ctx is done.
func startTokenMaintenance(ctx context.Context, tokens tokenPruner, log *logger.Logger) {
	go runTokenMaintenance(ctx, tokens, log, tokenPruneFirst, tokenPruneInterval)
}

func runTokenMaintenance(ctx context.Context, tokens tokenPruner, log *logger.Logger, first, every time.Duration) {
	timer := time.NewTimer(first)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			tokenPruneTick(ctx, tokens, log)
			timer.Reset(every)
		}
	}
}
