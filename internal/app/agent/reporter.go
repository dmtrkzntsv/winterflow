package agent

import (
	"context"
	"encoding/json"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/logger"
)

// StatusSource yields the current container status of all deployed apps. The
// Docker Compose orchestrator satisfies it.
type StatusSource interface {
	GetAppsStatus(ctx context.Context) ([]command.AppStatus, error)
}

// EventSink delivers one agent-initiated event upstream. The gRPC agent wraps
// SendEvent (distributed); standalone publishes straight onto the in-process
// events queue. A returned error means "not delivered this tick" — the
// reporter logs and retries on the next interval.
type EventSink func(kind bus.EventKind, payload []byte) error

// RunStatusReporter pushes an apps.status event immediately and then on every
// interval until ctx is done. It is the producer side of the live-status
// pipeline: events feed the API's status cache and are fanned out to browsers
// over SSE. Source errors (e.g. Docker briefly unavailable) skip the tick.
func RunStatusReporter(ctx context.Context, src StatusSource, sink EventSink, interval time.Duration, log *logger.Logger) {
	RunStatusReporterOn(ctx, src, sink, interval, nil, log)
}

// RunStatusReporterOn is RunStatusReporter that also reports whenever nudge
// delivers (a backup run ended, the backups were reloaded): a change the user
// has just been told about shows at once instead of at the next tick. Nudges
// that arrive while a report is being collected coalesce into one more; the
// status cache fans out only what changed. A nil nudge never fires.
func RunStatusReporterOn(ctx context.Context, src StatusSource, sink EventSink, interval time.Duration, nudge <-chan struct{}, log *logger.Logger) {
	report := func() {
		apps, err := src.GetAppsStatus(ctx)
		if err != nil {
			log.Debug("status report: collect failed", "error", err)
			return
		}
		payload, err := json.Marshal(command.GetAppsStatusResponse{Apps: apps})
		if err != nil {
			log.Error("status report: marshal failed", err)
			return
		}
		if err := sink(bus.EventAppsStatus, payload); err != nil {
			log.Debug("status report: send failed", "error", err)
		}
	}

	report()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report()
		case <-nudge:
			report()
		}
	}
}
