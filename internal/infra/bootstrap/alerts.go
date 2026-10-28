package bootstrap

import (
	"context"
	"sync"
	"time"

	"winterflow/internal/domain/service/alerting"
	alertrepo "winterflow/internal/infra/db/repository/alert"
	"winterflow/internal/infra/notify"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
	"winterflow/pkg/secretbox"
)

const (
	// alertMaintenanceInterval is how often default rules are seeded for
	// new orgs and the history is pruned.
	alertMaintenanceInterval = 10 * time.Minute
	// alertMaintenanceTimeout bounds one maintenance tick.
	alertMaintenanceTimeout = 2 * time.Minute
	// alertLoadTimeout bounds the evaluator's warm start at boot.
	alertLoadTimeout = 30 * time.Second
)

// legacyKeyWarning logs the JWT_SECRET deprecation once per process.
var legacyKeyWarning sync.Once

// alerts is the alerting wiring WireCore hands to the events sink and Deps.
type alerts struct {
	evaluator *alerting.Evaluator
	notifier  *alerting.Notifier
	// box encrypts channel URLs; nil without ENCRYPTION_KEY.
	box *secretbox.Box
}

// wireAlerts builds the channel box, the Shoutrrr sender, the notifier
// (draining on its own goroutine) and the evaluator, warms the evaluator
// from the stored states and the last hour of samples, and starts the
// maintenance loop. history is nil when metrics are off.
func wireAlerts(ctx context.Context, repo *alertrepo.Repo, history alerting.History, fan *statusFanout, cfg *config.ServerConfig, log *logger.Logger) alerts {
	key, legacy := cfg.GetEncryptionKey()
	if legacy {
		legacyKeyWarning.Do(func() {
			log.Warn(config.LegacyEncryptionKeyEnv + " is deprecated; rename it to ENCRYPTION_KEY")
		})
	}
	box, err := secretbox.New(key)
	if err != nil {
		log.Warn("notification channels unavailable: cannot build the channel URL cipher from ENCRYPTION_KEY", "error", err)
		box = nil
	}
	// A nil *Box must not become a non-nil Opener.
	var opener alerting.Opener
	if box != nil {
		opener = box
	}
	notifier := alerting.NewNotifier(repo, notify.NewShoutrrr(alerting.SendTimeout, cfg.AllowsPrivateAlertTargets()), opener, log)
	go notifier.Run(ctx)

	evaluator := alerting.NewEvaluator(alerting.Config{
		Store:     repo,
		History:   history,
		Queue:     notifier,
		Publish:   fan.alert,
		WebURL:    cfg.GetWebURL(),
		DownGrace: statusTTL,
		Log:       log,
	})
	lctx, cancel := context.WithTimeout(ctx, alertLoadTimeout)
	if err := loadEvaluator(evaluator, lctx); err != nil {
		log.Warn("alerts: warm start incomplete; stored firing alerts are re-read by the liveness sweep, "+
			"threshold rules may wait up to their duration for fresh samples", "error", err)
	}
	cancel()

	go runAlertMaintenance(ctx, repo, alertMaintenanceInterval, log)
	return alerts{evaluator: evaluator, notifier: notifier, box: box}
}

// loadEvaluator warms the evaluator. A variable so tests can observe when
// WireCore runs it.
var loadEvaluator = (*alerting.Evaluator).Load

// alertMaintainer is the slice of the alert repository the loop needs.
type alertMaintainer interface {
	SeedDefaults(ctx context.Context) (int, error)
	PruneEvents(ctx context.Context, now time.Time) (int64, error)
}

// alertMaintenanceTick seeds default rules for orgs that have none yet and
// prunes the history (90 days, 2,000 per org).
func alertMaintenanceTick(ctx context.Context, repo alertMaintainer, now time.Time, log *logger.Logger) {
	ctx, cancel := context.WithTimeout(ctx, alertMaintenanceTimeout)
	defer cancel()
	if n, err := repo.SeedDefaults(ctx); err != nil {
		log.Warn("alerts: seed default rules failed", "error", err)
	} else if n > 0 {
		log.Debug("alerts: seeded default rules", "orgs", n)
	}
	if n, err := repo.PruneEvents(ctx, now); err != nil {
		log.Warn("alerts: prune history failed", "error", err)
	} else if n > 0 {
		log.Debug("alerts: pruned history", "rows", n)
	}
}

// runAlertMaintenance ticks immediately (seeding orgs at startup) and then
// every interval until ctx is done.
func runAlertMaintenance(ctx context.Context, repo alertMaintainer, interval time.Duration, log *logger.Logger) {
	alertMaintenanceTick(ctx, repo, time.Now(), log)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			alertMaintenanceTick(ctx, repo, now, log)
		}
	}
}

// The evaluator is what the events sink drives.
var _ alertObserver = (*alerting.Evaluator)(nil)
