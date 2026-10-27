package bootstrap

import (
	"context"
	"time"

	"winterflow/pkg/logger"
)

const (
	// hourlyFirst is when the hourly jobs first run after start: soon, so a
	// process restarted more often than hourlyInterval still runs them.
	hourlyFirst = time.Minute
	// hourlyInterval is how often the hourly jobs run.
	hourlyInterval = time.Hour
	// hourlyJobTimeout bounds one run of one job.
	hourlyJobTimeout = time.Minute
)

// hourlyJob is one housekeeping task of the hourly loop: dead access tokens
// (revoked or expired more than 30 days ago) are deleted, stale command
// results marked unknown and old ones pruned.
type hourlyJob struct {
	name string
	run  func(ctx context.Context) error
}

// hourlyTick runs each job in turn, each bounded by hourlyJobTimeout; a
// failure is logged and the next job still runs.
func hourlyTick(ctx context.Context, jobs []hourlyJob, log *logger.Logger) {
	for _, job := range jobs {
		jctx, cancel := context.WithTimeout(ctx, hourlyJobTimeout)
		if err := job.run(jctx); err != nil {
			log.Warn(job.name+" failed", "error", err)
		}
		cancel()
	}
}

// startHourlyMaintenance runs jobs a minute after start, then every hour,
// on one goroutine until ctx is done.
func startHourlyMaintenance(ctx context.Context, log *logger.Logger, jobs ...hourlyJob) {
	go runHourlyMaintenance(ctx, jobs, log, hourlyFirst, hourlyInterval)
}

func runHourlyMaintenance(ctx context.Context, jobs []hourlyJob, log *logger.Logger, first, every time.Duration) {
	timer := time.NewTimer(first)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			hourlyTick(ctx, jobs, log)
			timer.Reset(every)
		}
	}
}
