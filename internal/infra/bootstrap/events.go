package bootstrap

import (
	"context"
	"encoding/json"
	"time"

	"winterflow/internal/domain/alert"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/metrics"
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

// eventStore is the slice of the server repository the event sink needs.
type eventStore interface {
	TouchLastSeen(ctx context.Context, serverID string) error
	SaveCapabilities(ctx context.Context, serverID string, capabilities map[string]string, features map[string]bool) error
}

// metricsStore is the slice of the metric repository the event sink needs.
type metricsStore interface {
	InsertSample(ctx context.Context, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values) error
}

// metricsInsertTimeout bounds one sample write.
const metricsInsertTimeout = 10 * time.Second

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

// alert pushes an alert transition to the server's org.
func (f *statusFanout) alert(ctx context.Context, serverID string, n alert.Notice) {
	f.send(ctx, serverID, model.Notification{Type: model.NotificationAlert, Payload: n, Timestamp: time.Now()})
}

func (f *statusFanout) appsStatus(ctx context.Context, serverID string, apps []command.AppStatus) {
	f.send(ctx, serverID, model.Notification{
		Type:      model.NotificationAppsStatus,
		Payload:   command.AppsStatusPayload{ServerID: serverID, Apps: apps},
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
	// metrics stores server.metrics samples; nil when METRICS_ENABLED=false
	// (the events are then dropped). latest keeps each server's newest host
	// sample for the dashboard.
	metrics metricsStore
	latest  *metrics.LatestStore
	// alerts, when set, evaluates the alert rules on every event and on
	// the sweep.
	alerts alertObserver
	// sync, when set, fills the API's per-server indexes from the agent: it
	// starts when a server comes online (an unknown->online transition, or a
	// capabilities event: the agent (re)registered) and is nudged by every
	// heartbeat while it is still pending.
	sync serverSync
	// vars, when set, receives the shared-variable signals: key changes,
	// reported snapshot revisions and reload outcomes.
	vars varsEvents
}

// alertObserver is the alert evaluator as the event sink drives it
// (alerting.Evaluator).
type alertObserver interface {
	ObserveHeartbeat(ctx context.Context, serverID string)
	ObserveMetrics(ctx context.Context, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values)
	ObserveAppStatus(ctx context.Context, serverID string, problematic map[string]bool)
	Sweep(ctx context.Context)
}

// serverSync is the per-server index fill the event sink drives.
type serverSync interface {
	online(serverID string)
	heartbeat(serverID string)
}

// varsEvents are the shared-variable hooks of the event sink.
type varsEvents interface {
	capabilities(ctx context.Context, serverID string, caps map[string]string)
	revision(ctx context.Context, serverID string, agentRev int64)
	reloaded(ctx context.Context, serverID string, payload []byte)
}

// varsDriver is varsSync as the subscriber sees it: an online sync plus
// the event hooks.
type varsDriver interface {
	serverSync
	varsEvents
}

// fanSync drives several per-server syncs from the same online/heartbeat
// signals.
type fanSync []serverSync

func (f fanSync) online(serverID string) {
	for _, s := range f {
		s.online(serverID)
	}
}

func (f fanSync) heartbeat(serverID string) {
	for _, s := range f {
		s.heartbeat(serverID)
	}
}

func (s *eventSink) handle(ctx context.Context, ev bus.EventMessage) {
	var transition bool
	now := time.Now()
	switch ev.Kind {
	case bus.EventServerOnline:
		transition = s.cache.MarkOnline(ev.ServerID, now)
		if err := s.store.TouchLastSeen(ctx, ev.ServerID); err != nil {
			s.log.Debug("touch last_seen failed", "server_id", ev.ServerID, "error", err)
		}
		if transition {
			s.fan.serverStatus(ctx, ev.ServerID, status.LivenessOnline)
		}
		if s.alerts != nil {
			s.alerts.ObserveHeartbeat(ctx, ev.ServerID)
		}
	case bus.EventCapabilities:
		var c capabilitiesEvent
		if err := json.Unmarshal(ev.Payload, &c); err != nil {
			s.log.Error("failed to decode capabilities event", err)
			return
		}
		if s.vars != nil {
			s.vars.capabilities(ctx, ev.ServerID, c.Capabilities)
		}
		transition = s.cache.MarkOnline(ev.ServerID, now)
		if err := s.store.SaveCapabilities(ctx, ev.ServerID, c.Capabilities, c.Features); err != nil {
			s.log.Debug("save capabilities failed", "server_id", ev.ServerID, "error", err)
		}
		if transition {
			s.fan.serverStatus(ctx, ev.ServerID, status.LivenessOnline)
		}
		if s.alerts != nil {
			s.alerts.ObserveHeartbeat(ctx, ev.ServerID)
		}
	case bus.EventAppsStatus:
		var body command.GetAppsStatusResponse
		if err := json.Unmarshal(ev.Payload, &body); err != nil {
			s.log.Error("failed to decode apps status event", err)
			return
		}
		// Agents predating shared variables omit vars_revision: never
		// drift-push to them.
		if s.vars != nil && body.VarsRevision != nil {
			s.vars.revision(ctx, ev.ServerID, *body.VarsRevision)
		}
		var changed bool
		changed, transition = s.cache.SetAppReport(ev.ServerID, body.Apps, body.VarsRevision, now)
		// The reporter pushes on a fixed timer; only actual changes are worth
		// a DB lookup + SSE fan-out. New clients seed from the cache on load.
		if changed {
			s.fan.appsStatus(ctx, ev.ServerID, body.Apps)
		}
		if transition {
			s.fan.serverStatus(ctx, ev.ServerID, status.LivenessOnline)
		}
		// Every report counts (time passes for app-down rules), not only
		// changed ones.
		if s.alerts != nil {
			s.alerts.ObserveAppStatus(ctx, ev.ServerID, problematicApps(body.Apps))
		}
	case bus.EventServerMetrics:
		s.storeMetrics(ctx, ev, now)
		return
	case bus.EventVarsReloaded:
		// A reload outcome is not a liveness or status signal.
		if s.vars != nil {
			s.vars.reloaded(ctx, ev.ServerID, ev.Payload)
		}
		return
	default:
		s.log.Warn("unknown event kind", "kind", ev.Kind)
		return
	}
	if s.sync == nil {
		return
	}
	if transition || ev.Kind == bus.EventCapabilities {
		s.sync.online(ev.ServerID)
	} else if ev.Kind == bus.EventServerOnline {
		s.sync.heartbeat(ev.ServerID)
	}
}

// storeMetrics writes one server.metrics sample (tier 1) and remembers the
// host part as the server's latest. A write failure is logged; the next
// minute's sample is independent. Metrics never touch liveness or SSE.
func (s *eventSink) storeMetrics(ctx context.Context, ev bus.EventMessage, now time.Time) {
	if s.metrics == nil {
		// Metrics are off: nothing to store or evaluate, but the event
		// still proves the server is up.
		if s.alerts != nil {
			s.alerts.ObserveHeartbeat(ctx, ev.ServerID)
		}
		return
	}
	var sample metrics.Sample
	if err := json.Unmarshal(ev.Payload, &sample); err != nil {
		s.log.Warn("failed to decode metrics event", "server_id", ev.ServerID, "error", err)
		return
	}
	at := metrics.SampleTime(sample, now)
	apps := make(map[string]metrics.Values, len(sample.Apps))
	for _, a := range sample.Apps {
		if a.AppID != "" {
			apps[a.AppID] = a.Values()
		}
	}
	// An empty server map writes no server row (the repository skips it).
	if server := sample.Server.Values(); len(server) > 0 || len(apps) > 0 {
		wctx, cancel := context.WithTimeout(ctx, metricsInsertTimeout)
		err := s.metrics.InsertSample(wctx, ev.ServerID, at, server, apps)
		cancel()
		if err != nil {
			s.log.Warn("store metrics failed", "server_id", ev.ServerID, "error", err)
		}
	}
	if s.latest != nil {
		s.latest.Set(ev.ServerID, metrics.Latest{At: at, Server: sample.Server})
	}
	if s.alerts != nil {
		s.alerts.ObserveMetrics(ctx, ev.ServerID, at, sample.Server.Values(), apps)
	}
}

// problematicApps maps an apps.status report to app id -> problematic.
func problematicApps(apps []command.AppStatus) map[string]bool {
	out := make(map[string]bool, len(apps))
	for _, a := range apps {
		out[a.AppID] = a.StatusCode == command.ContainerStatusProblematic
	}
	return out
}

// sweep flips servers whose liveness expired to unknown and pushes the
// transition. Factored out of the ticker loop for testability.
func (s *eventSink) sweep(ctx context.Context, cache *status.Cache, now time.Time) {
	for _, serverID := range cache.ExpireStale(now) {
		s.fan.serverStatus(ctx, serverID, status.LivenessUnknown)
	}
	if s.alerts != nil {
		s.alerts.Sweep(ctx)
	}
}

// startEventsSubscriber drains the region's events queue (agent-initiated:
// liveness, capabilities, status), updating the in-memory status cache and the
// DB, and pushing transitions over SSE. It also runs the expiry sweeper. It is
// the API's consumer of events:<region>.
func startEventsSubscriber(ctx context.Context, b bus.Bus, cache *status.Cache, serverRepo port.ServerRepository, nm port.NotificationManager, cfg *config.ServerConfig, log *logger.Logger, indexSync serverSync, vars varsDriver, ms metricsStore, latest *metrics.LatestStore, alerts alertObserver) {
	sink := &eventSink{
		cache:   cache,
		store:   serverRepo,
		fan:     newStatusFanout(serverRepo, nm, log),
		log:     log,
		sync:    indexSync,
		metrics: ms,
		latest:  latest,
		alerts:  alerts,
	}
	if vars != nil {
		sink.vars = vars
		sink.sync = fanSync{indexSync, vars}
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
