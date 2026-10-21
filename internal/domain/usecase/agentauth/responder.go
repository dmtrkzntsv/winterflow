package agentauth

import (
	"context"
	"encoding/json"
	"time"

	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// checkTimeout bounds one check (and its reply publish).
const checkTimeout = 3 * time.Second

// StartResponder subscribes to the hub request channel and answers each
// server.verify request on the asking hub's response channel. Unknown request
// types are logged and ignored; a check that errors sends no reply. The
// consume loop runs until ctx is done.
func StartResponder(ctx context.Context, b bus.Bus, cfg *config.ServerConfig, repo store, log *logger.Logger) error {
	msgs, cancel, err := b.Subscribe(ctx, cfg.GetHubRequestsChannel())
	if err != nil {
		return err
	}
	go func() {
		defer func() { _ = cancel() }()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-msgs:
				if !ok {
					return
				}
				var req bus.HubRequest
				if err := json.Unmarshal([]byte(msg.Payload), &req); err != nil {
					log.Error("failed to unmarshal hub request", err)
					continue
				}
				answer(ctx, b, repo, req, log)
			}
		}
	}()
	return nil
}

func answer(ctx context.Context, b bus.Bus, repo store, req bus.HubRequest, log *logger.Logger) {
	if req.Type != bus.HubRequestVerify {
		log.Warn("ignoring hub request of unknown type", "type", req.Type)
		return
	}
	if req.HubID == "" || req.RequestID == "" {
		log.Warn("ignoring hub request without hub_id or request_id")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	verdict, reason, err := Check(ctx, repo, req.ServerID, req.CertFingerprint)
	if err != nil {
		log.Error("agent auth check failed", err, "server_id", req.ServerID)
		return
	}
	resp := bus.HubResponse{RequestID: req.RequestID, Verdict: verdict, Reason: reason}
	if err := b.Publish(ctx, config.HubResponsesChannel(req.HubID), resp); err != nil {
		log.Error("failed to publish agent auth reply", err, "hub_id", req.HubID)
	}
}

// BusRevoker announces a server's revocation to every hub. It satisfies
// port.Revoker.
type BusRevoker struct {
	bus     bus.Bus
	channel string
}

// NewBusRevoker builds the revoker over b and the config's broadcast channel.
func NewBusRevoker(b bus.Bus, cfg *config.ServerConfig) *BusRevoker {
	return &BusRevoker{bus: b, channel: cfg.GetHubBroadcastChannel()}
}

// Revoke publishes server.revoked for serverID, bounded by 3 s.
func (r *BusRevoker) Revoke(ctx context.Context, serverID string) error {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	return r.bus.Publish(ctx, r.channel, bus.HubBroadcast{Type: bus.HubBroadcastRevoked, ServerID: serverID})
}
