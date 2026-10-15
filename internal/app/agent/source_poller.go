package agent

import (
	"context"
	"time"

	"winterflow/internal/domain/port"
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
// reload (may be nil; production passes AfterSourceUpdate) runs after a
// tick that updated apps: the upstream may have changed an app's backup
// recipe or its expose ports.
func RunSourcePoller(ctx context.Context, orch SourceRefresher, reload func(), log *logger.Logger) {
	runSourcePoller(ctx, orch, reload, sourcePollTick, log)
}

func runSourcePoller(ctx context.Context, orch SourceRefresher, reload func(), tick time.Duration, log *logger.Logger) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if updated := orch.RefreshDueSources(ctx); len(updated) > 0 {
				log.Info("auto-updated git-sourced apps", "apps", updated)
				if reload != nil {
					reload()
				}
			}
		}
	}
}

// AfterSourceUpdate is the poller's reload: an upstream move can change an
// app's backup recipe (the scheduler's plan is reread) and move, add or drop
// its http expose ports, which ingress hostnames resolve to (the proxy
// config is rebuilt; an unchanged one is not reloaded).
func AfterSourceUpdate(ctx context.Context, reloadBackups func(), ingress port.IngressManager, log *logger.Logger) func() {
	return func() {
		if reloadBackups != nil {
			reloadBackups()
		}
		if ingress == nil {
			return
		}
		for _, w := range ingress.Reload(ctx) {
			log.Warn("ingress", "warning", w)
		}
	}
}
