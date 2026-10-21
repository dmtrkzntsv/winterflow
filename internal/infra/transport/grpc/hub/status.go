package hub

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/hubs"
	"winterflow/pkg/version"
)

// withdrawTimeout bounds the best-effort removal of the hub's entry on
// shutdown.
const withdrawTimeout = 3 * time.Second

// StartAnnouncer lists the hub in reg: once at start, then every
// hubs.AnnounceEvery with a hubs.AnnounceTTL expiry, and at once when the
// hub starts or stops draining. When ctx ends it
// removes the entry (best effort) so the hub leaves the list at once; the
// returned channel closes after that.
//
// reg is also the list a stopping hub reads to learn whether another hub
// can take its agents (Drain).
func (h *Hub) StartAnnouncer(ctx context.Context, reg port.HubRegistry) <-chan struct{} {
	h.drainMu.Lock()
	h.peers = reg
	h.drainMu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.startAnnouncer(ctx, reg)
	}()
	return done
}

func (h *Hub) startAnnouncer(ctx context.Context, reg port.HubRegistry) {
	ticker := time.NewTicker(hubs.AnnounceEvery)
	defer ticker.Stop()
	for {
		if err := reg.Announce(ctx, h.status(), hubs.AnnounceTTL); err != nil && ctx.Err() == nil {
			h.log.Warn("hub announce failed", "error", err)
		}
		select {
		case <-h.announceNow:
			continue
		case <-ctx.Done():
			wctx, cancel := context.WithTimeout(context.Background(), withdrawTimeout)
			defer cancel()
			if err := reg.Withdraw(wctx, h.id); err != nil {
				h.log.Warn("hub withdraw failed", "error", err)
			}
			return
		case <-ticker.C:
		}
	}
}

// status is the hub's entry in the hub list; ConnectedAgents counts the
// agents with an active stream.
func (h *Hub) status() model.Hub {
	return model.Hub{
		ID:              h.id,
		PublicAddr:      h.publicAddr,
		Version:         version.GetVersion(),
		Capacity:        h.cfg.GetHubCapacity(),
		ConnectedAgents: h.activeStreams(),
		Accepting:       h.cfg.GetHubAccepting(),
		Draining:        h.isDraining(),
	}
}
