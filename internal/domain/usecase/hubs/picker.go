// Package hubs assigns enrolling servers to hubs and routes each command to
// the request queue of the hub its server is assigned to.
package hubs

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

const (
	// AnnounceEvery is how often a hub renews its entry in the hub list.
	AnnounceEvery = 30 * time.Second
	// AnnounceTTL is how long an entry lives without a renewal (three
	// missed announcements).
	AnnounceTTL = 90 * time.Second
)

// ErrNoHub: no live hub accepts new servers.
var ErrNoHub = errors.New("no hub is available, try again in a minute")

// HubCounts is the slice of port.ServerRepository the picker needs.
type HubCounts interface {
	CountServersByHub(ctx context.Context) (map[string]int, error)
}

// Picker chooses the hub a newly enrolled server is assigned to.
type Picker struct {
	registry port.HubRegistry
	servers  HubCounts
	log      *logger.Logger

	mu   sync.Mutex // rand.Rand is not safe for concurrent use
	rand *rand.Rand
}

// NewPicker builds a picker over the live hub list and the servers table.
func NewPicker(registry port.HubRegistry, servers HubCounts, log *logger.Logger) *Picker {
	return &Picker{
		registry: registry,
		servers:  servers,
		log:      log,
		rand:     rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
	}
}

// Pick returns a random live hub that accepts new servers and is below its
// capacity (servers assigned). When every such hub is full it returns the
// one with the lowest assigned/capacity ratio and logs a warning. No hub
// accepting new servers is ErrNoHub.
func (p *Picker) Pick(ctx context.Context) (model.Hub, error) {
	return p.PickExcept(ctx, "")
}

// PickExcept is Pick over every hub but hubID (a server leaving its hub).
func (p *Picker) PickExcept(ctx context.Context, hubID string) (model.Hub, error) {
	listed, err := p.registry.List(ctx)
	if err != nil {
		return model.Hub{}, fmt.Errorf("list hubs: %w", err)
	}
	counts, err := p.servers.CountServersByHub(ctx)
	if err != nil {
		return model.Hub{}, fmt.Errorf("count servers by hub: %w", err)
	}
	var open, roomy []model.Hub
	for _, h := range listed {
		if !h.Accepting || h.Draining || (hubID != "" && h.ID == hubID) {
			continue
		}
		h.Assigned = counts[h.ID]
		open = append(open, h)
		if h.Assigned < h.Capacity {
			roomy = append(roomy, h)
		}
	}
	if len(roomy) > 0 {
		p.mu.Lock()
		i := p.rand.IntN(len(roomy))
		p.mu.Unlock()
		return roomy[i], nil
	}
	if len(open) == 0 {
		return model.Hub{}, ErrNoHub
	}
	best := open[0]
	for _, h := range open[1:] {
		if lessLoaded(h, best) {
			best = h
		}
	}
	p.log.Warn("every hub is at capacity; assigning the least loaded",
		"hub_id", best.ID, "assigned", best.Assigned, "capacity", best.Capacity)
	return best, nil
}

// lessLoaded reports whether a's assigned/capacity ratio is below b's,
// compared by cross-multiplication; a hub without capacity loses.
func lessLoaded(a, b model.Hub) bool {
	if a.Capacity <= 0 {
		return false
	}
	if b.Capacity <= 0 {
		return true
	}
	return a.Assigned*b.Capacity < b.Assigned*a.Capacity
}
