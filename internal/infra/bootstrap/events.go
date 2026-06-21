package bootstrap

import (
	"context"
	"encoding/json"
	"time"

	"winterflow/internal/domain/command"
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

// capabilitiesEvent mirrors the JSON the Hub publishes for EventCapabilities.
type capabilitiesEvent struct {
	Capabilities map[string]string `json:"capabilities"`
	Features     map[string]bool   `json:"features"`
}

// startEventsSubscriber drains the region's events queue (agent-initiated:
// liveness, capabilities, status), updating the in-memory status cache and the
// DB (capabilities/last_seen). It is the API's consumer of events:<region>.
func startEventsSubscriber(ctx context.Context, b bus.Bus, cache *status.Cache, serverRepo port.ServerRepository, cfg *config.ServerConfig, log *logger.Logger) {
	go func() {
		msgs, cancel, err := b.Subscribe(ctx, cfg.GetBusEventsQueue())
		if err != nil {
			log.Fatalf("failed to subscribe to events queue: %v", err)
		}
		defer cancel()
		for msg := range msgs {
			var ev bus.EventMessage
			if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
				log.Error("failed to unmarshal event", err)
				continue
			}
			handleEvent(ctx, ev, cache, serverRepo, log)
		}
	}()
	log.Debug("events subscriber started", "events_queue", cfg.GetBusEventsQueue())
}

func handleEvent(ctx context.Context, ev bus.EventMessage, cache *status.Cache, serverRepo port.ServerRepository, log *logger.Logger) {
	now := time.Now()
	switch ev.Kind {
	case bus.EventServerOnline:
		cache.MarkOnline(ev.ServerID, now)
		if err := serverRepo.TouchLastSeen(ctx, ev.ServerID); err != nil {
			log.Debug("touch last_seen failed", "server_id", ev.ServerID, "error", err)
		}
	case bus.EventCapabilities:
		var c capabilitiesEvent
		if err := json.Unmarshal(ev.Payload, &c); err != nil {
			log.Error("failed to decode capabilities event", err)
			return
		}
		cache.MarkOnline(ev.ServerID, now)
		if err := serverRepo.SaveCapabilities(ctx, ev.ServerID, c.Capabilities, c.Features); err != nil {
			log.Debug("save capabilities failed", "server_id", ev.ServerID, "error", err)
		}
	case bus.EventAppsStatus:
		var s command.GetAppsStatusResponse
		if err := json.Unmarshal(ev.Payload, &s); err != nil {
			log.Error("failed to decode apps status event", err)
			return
		}
		cache.SetAppStatus(ev.ServerID, s.Apps, now)
	default:
		log.Warn("unknown event kind", "kind", ev.Kind)
	}
}
