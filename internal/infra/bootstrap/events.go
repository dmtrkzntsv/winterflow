package bootstrap

import (
	"context"
	"encoding/json"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// statusTTL is the freshness window for the in-memory status cache. Sized to a
// small multiple of the agent heartbeat interval (30s) so a missed beat or two
// doesn't immediately flip a server to "unknown".
const statusTTL = 90 * time.Second

// sweepInterval is how often expired liveness entries are swept into
// "unknown" transitions pushed over SSE.
const sweepInterval = 15 * time.Second

// capabilitiesEvent mirrors the JSON the Hub publishes for EventCapabilities.
type capabilitiesEvent struct {
	Capabilities map[string]string `json:"capabilities"`
	Features     map[string]bool   `json:"features"`
}

// appsStatusPayload is the apps_status notification body pushed to browsers.
type appsStatusPayload struct {
	ServerID string              `json:"server_id"`
	Apps     []command.AppStatus `json:"apps"`
}

// eventStore is the slice of the server repository the event sink needs.
type eventStore interface {
	TouchLastSeen(ctx context.Context, serverID string) error
	SaveCapabilities(ctx context.Context, serverID string, capabilities map[string]string, features map[string]bool) error
}

// statusFanout pushes an unsolicited status notification to every member of
// the org that owns the server. Function fields keep it trivially fakeable.
type statusFanout struct {
	userIDs func(ctx context.Context, serverID string) ([]string, error)
	publish func(userID string, n model.Notification)
	log     *logger.Logger
}

func newStatusFanout(repo port.ServerRepository, nm port.NotificationManager, log *logger.Logger) *statusFanout {
	return &statusFanout{userIDs: repo.GetServerUserIDs, publish: nm.Publish, log: log}
}

func (f *statusFanout) send(ctx context.Context, serverID string, n model.Notification) {
	users, err := f.userIDs(ctx, serverID)
	if err != nil {
		f.log.Debug("status fan-out: resolve users failed", "server_id", serverID, "error", err)
		return
	}
	for _, uid := range users {
		f.publish(uid, n)
	}
}

func (f *statusFanout) serverStatus(ctx context.Context, serverID string, liveness status.Liveness) {
	f.send(ctx, serverID, model.Notification{
		Type:      model.NotificationServerStatus,
		Payload:   model.ServerStatusPayload{ServerID: serverID, Liveness: string(liveness)},
		Timestamp: time.Now(),
	})
}

func (f *statusFanout) appsStatus(ctx context.Context, serverID string, apps []command.AppStatus) {
	f.send(ctx, serverID, model.Notification{
		Type:      model.NotificationAppsStatus,
		Payload:   appsStatusPayload{ServerID: serverID, Apps: apps},
		Timestamp: time.Now(),
	})
}

// eventSink applies agent-initiated events to the status cache and the DB, and
// pushes liveness/app-status transitions to browsers over SSE.
type eventSink struct {
	cache *status.Cache
	store eventStore
	fan   *statusFanout
	log   *logger.Logger
}

func (s *eventSink) handle(ctx context.Context, ev bus.EventMessage) {
	now := time.Now()
	switch ev.Kind {
	case bus.EventServerOnline:
		transition := s.cache.MarkOnline(ev.ServerID, now)
		if err := s.store.TouchLastSeen(ctx, ev.ServerID); err != nil {
			s.log.Debug("touch last_seen failed", "server_id", ev.ServerID, "error", err)
		}
		if transition {
			s.fan.serverStatus(ctx, ev.ServerID, status.LivenessOnline)
		}
	case bus.EventCapabilities:
		var c capabilitiesEvent
		if err := json.Unmarshal(ev.Payload, &c); err != nil {
			s.log.Error("failed to decode capabilities event", err)
			return
		}
		transition := s.cache.MarkOnline(ev.ServerID, now)
		if err := s.store.SaveCapabilities(ctx, ev.ServerID, c.Capabilities, c.Features); err != nil {
			s.log.Debug("save capabilities failed", "server_id", ev.ServerID, "error", err)
		}
		if transition {
			s.fan.serverStatus(ctx, ev.ServerID, status.LivenessOnline)
		}
	case bus.EventAppsStatus:
		var body command.GetAppsStatusResponse
		if err := json.Unmarshal(ev.Payload, &body); err != nil {
			s.log.Error("failed to decode apps status event", err)
			return
		}
		transition := s.cache.SetAppStatus(ev.ServerID, body.Apps, now)
		s.fan.appsStatus(ctx, ev.ServerID, body.Apps)
		if transition {
			s.fan.serverStatus(ctx, ev.ServerID, status.LivenessOnline)
		}
	default:
		s.log.Warn("unknown event kind", "kind", ev.Kind)
	}
}

// sweep flips servers whose liveness expired to unknown and pushes the
// transition. Factored out of the ticker loop for testability.
func (s *eventSink) sweep(ctx context.Context, cache *status.Cache, now time.Time) {
	for _, serverID := range cache.ExpireStale(now) {
		s.fan.serverStatus(ctx, serverID, status.LivenessUnknown)
	}
}

// startEventsSubscriber drains the region's events queue (agent-initiated:
// liveness, capabilities, status), updating the in-memory status cache and the
// DB, and pushing transitions over SSE. It also runs the expiry sweeper. It is
// the API's consumer of events:<region>.
func startEventsSubscriber(ctx context.Context, b bus.Bus, cache *status.Cache, serverRepo port.ServerRepository, nm port.NotificationManager, cfg *config.ServerConfig, log *logger.Logger) {
	sink := &eventSink{
		cache: cache,
		store: serverRepo,
		fan:   newStatusFanout(serverRepo, nm, log),
		log:   log,
	}
	bus.SubscribeJSON(ctx, b, cfg.GetBusEventsQueue(), log, func(ev bus.EventMessage) {
		sink.handle(ctx, ev)
	})
	go func() {
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				sink.sweep(ctx, cache, now)
			}
		}
	}()
	log.Debug("events subscriber started", "events_queue", cfg.GetBusEventsQueue())
}
