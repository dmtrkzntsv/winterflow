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

// VarsRevisionSource is implemented by status sources that hold a
// shared-variables snapshot; the report then carries its revision, so the
// API can re-push a missed vars.sync without a polling timer.
type VarsRevisionSource interface {
	VarsRevision() int64
}

// statusReport is the apps.status body for apps from src.
func statusReport(apps []command.AppStatus, src StatusSource) command.GetAppsStatusResponse {
	resp := command.GetAppsStatusResponse{Apps: apps}
	// A StatusSnapshot (shared with the metrics reporter) wraps the real
	// source; the revision lives on the wrapped one.
	if w, ok := src.(*StatusSnapshot); ok {
		src = w.src
	}
	if vr, ok := src.(VarsRevisionSource); ok {
		rev := vr.VarsRevision()
		resp.VarsRevision = &rev
	}
	return resp
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
	report := func() {
		apps, err := src.GetAppsStatus(ctx)
		if err != nil {
			log.Debug("status report: collect failed", "error", err)
			return
		}
		payload, err := json.Marshal(statusReport(apps, src))
		if err != nil {
			log.Error("status report: marshal failed", "error", err)
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
		}
	}
}
