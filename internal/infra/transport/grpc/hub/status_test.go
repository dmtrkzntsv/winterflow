package hub

import (
	"context"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/logger"
)

// recordingRegistry records announcements and withdrawals.
type recordingRegistry struct {
	mu        sync.Mutex
	announced chan model.Hub
	ttls      []time.Duration
	withdrawn []string
}

func newRecordingRegistry() *recordingRegistry {
	return &recordingRegistry{announced: make(chan model.Hub, 16)}
}

func (r *recordingRegistry) Announce(_ context.Context, h model.Hub, ttl time.Duration) error {
	r.mu.Lock()
	r.ttls = append(r.ttls, ttl)
	r.mu.Unlock()
	r.announced <- h
	return nil
}

func (r *recordingRegistry) Withdraw(ctx context.Context, id string) error {
	if _, ok := ctx.Deadline(); !ok {
		panic("withdraw without a deadline")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.withdrawn = append(r.withdrawn, id)
	return nil
}

func (r *recordingRegistry) List(context.Context) ([]model.Hub, error) { return nil, nil }

func TestAnnouncerAnnounces(t *testing.T) {
	t.Setenv("HUB_CAPACITY", "250")
	h := newTestHub(t, nil)
	h.agents["a1"] = &agent{streamActive: true}
	h.agents["a2"] = &agent{streamActive: true}
	h.agents["a3"] = &agent{streamActive: false} // registered, no stream yet

	reg := newRecordingRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	done := h.StartAnnouncer(ctx, reg)

	select {
	case got := <-reg.announced:
		want := model.Hub{
			ID: "hub-01.winterflow.dev", PublicAddr: "hub-01.winterflow.dev:50051", Version: got.Version,
			Capacity: 250, ConnectedAgents: 2, Accepting: true,
		}
		if got != want || got.Version == "" {
			t.Fatalf("announced %+v, want %+v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no announcement at start")
	}
	reg.mu.Lock()
	if reg.ttls[0] != 90*time.Second {
		t.Fatalf("ttl = %v", reg.ttls[0])
	}
	reg.mu.Unlock()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("announcer did not stop")
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if len(reg.withdrawn) != 1 || reg.withdrawn[0] != "hub-01.winterflow.dev" {
		t.Fatalf("withdrawn = %v", reg.withdrawn)
	}
}

func TestAnnouncerNotAccepting(t *testing.T) {
	t.Setenv("HUB_ACCEPTING", "false")
	h := newTestHub(t, nil)
	if got := h.status(); got.Accepting || got.Capacity != 1000 || got.ConnectedAgents != 0 {
		t.Fatalf("status = %+v", got)
	}
}

func TestStartBusBridgeNeedsHubID(t *testing.T) {
	h := newTestHub(t, membus.NewBus(quietLog()))
	h.id, h.publicAddr = "", "" // HUB_PUBLIC_ADDR was not host:port at NewHub
	if err := h.StartBusBridge(context.Background()); err == nil {
		t.Fatal("bridge without a hub id: want error")
	}
}

func quietLog() *logger.Logger {
	return logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
}
