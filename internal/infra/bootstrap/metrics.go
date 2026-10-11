package bootstrap

import (
	"context"
	"time"

	"winterflow/internal/domain/metrics"
	"winterflow/pkg/logger"
)

const (
	// metricsMaintenanceInterval is how often closed buckets are rolled up.
	metricsMaintenanceInterval = 10 * time.Minute
	// metricsPruneEvery is how often rows past retention are deleted.
	metricsPruneEvery = time.Hour
	// metricsMaintenanceTimeout bounds one tick's DB work.
	metricsMaintenanceTimeout = 2 * time.Minute
	// latestTTL drops a server's latest sample once it stopped reporting
	// for this long (there is no server-delete path to hook instead).
	latestTTL = time.Hour
)

// metricsMaintainer is the slice of the metric repository the loop needs.
type metricsMaintainer interface {
	Rollup(ctx context.Context, now time.Time) (int, error)
	Prune(ctx context.Context, now time.Time) (int64, error)
}

// metricsMaintenance rolls up closed buckets on every tick, prunes past
// retention at most once per metricsPruneEvery, and drops stale latest
// samples. Rollup runs first so a bucket is never pruned before it was
// rolled up.
type metricsMaintenance struct {
	repo      metricsMaintainer
	latest    *metrics.LatestStore
	log       *logger.Logger
	lastPrune time.Time
}

func (m *metricsMaintenance) tick(ctx context.Context, now time.Time) {
	ctx, cancel := context.WithTimeout(ctx, metricsMaintenanceTimeout)
	defer cancel()
	if n, err := m.repo.Rollup(ctx, now); err != nil {
		m.log.Warn("metrics rollup failed", "error", err)
	} else if n > 0 {
		m.log.Debug("metrics rolled up", "rows", n)
	}
	if now.Sub(m.lastPrune) >= metricsPruneEvery {
		if n, err := m.repo.Prune(ctx, now); err != nil {
			m.log.Warn("metrics prune failed", "error", err)
		} else {
			m.lastPrune = now
			m.log.Debug("metrics pruned", "rows", n)
		}
	}
	m.latest.Prune(now.Add(-latestTTL))
}

// runMetricsMaintenance ticks immediately (catching up after downtime) and
// then every interval until ctx is done.
func runMetricsMaintenance(ctx context.Context, repo metricsMaintainer, latest *metrics.LatestStore, interval time.Duration, log *logger.Logger) {
	m := &metricsMaintenance{repo: repo, latest: latest, log: log}
	m.tick(ctx, time.Now())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.tick(ctx, now)
		}
	}
}
