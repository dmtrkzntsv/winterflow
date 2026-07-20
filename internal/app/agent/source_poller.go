package agent

import (
	"context"
	"time"

	"winterflow/pkg/logger"
)

// SourceRefresher is the orchestrator's upstream-polling surface.
type SourceRefresher interface {
	RefreshDueSources(ctx context.Context) []string
}

// sourcePollTick is how often the poller wakes to check for due apps; each
// app's own poll interval gates the actual upstream fetch.
const sourcePollTick = 30 * time.Second

// RunSourcePoller drives auto-update for git-sourced apps until ctx is done.
func RunSourcePoller(ctx context.Context, orch SourceRefresher, log *logger.Logger) {
	runSourcePoller(ctx, orch, sourcePollTick, log)
}

func runSourcePoller(ctx context.Context, orch SourceRefresher, tick time.Duration, log *logger.Logger) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if updated := orch.RefreshDueSources(ctx); len(updated) > 0 {
				log.Info("auto-updated git-sourced apps", "apps", updated)
			}
		}
	}
}
