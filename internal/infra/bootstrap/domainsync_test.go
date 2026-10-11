package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/logger"
)

// fakeHub answers domain.list the way the hub does: "agent not connected"
// until the agent's stream is up, then the agent's registrations. drop
// loses results (no answer at all); fail makes the publish itself fail;
// agentErr is a definitive agent-side error.
type fakeHub struct {
	mu        sync.Mutex
	connected bool
	drop      bool
	fail      bool
	agentErr  string
	calls     int
}

func (h *fakeHub) set(f func(h *fakeHub)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f(h)
}

func (h *fakeHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

func (h *fakeHub) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	h.mu.Lock()
	h.calls++
	connected, drop, fail, agentErr := h.connected, h.drop, h.fail, h.agentErr
	h.mu.Unlock()
	if in.Type != command.TypeDomainList || in.UserID != "" {
		return "", errors.New("unexpected command")
	}
	switch {
	case fail:
		return "", errors.New("bus down")
	case drop:
	case !connected:
		in.OnResult(port.CommandResult{Error: bus.ErrAgentNotConnected})
	case agentErr != "":
		in.OnResult(port.CommandResult{Error: agentErr})
	default:
		raw, _ := json.Marshal(command.ListDomainsResponse{Domains: []command.Domain{{RegisteredDomain: model.RegisteredDomain{Name: "a.app", TLS: model.DomainTLSAuto}}}})
		in.OnResult(port.CommandResult{Success: true, Payload: raw})
	}
	return "req", nil
}

// fakeIndex records ReplaceForServer calls.
type fakeIndex struct {
	mu       sync.Mutex
	replaced map[string][]model.RegisteredDomain
}

func (f *fakeIndex) ListForServer(context.Context, string) ([]model.RegisteredDomain, error) {
	return nil, nil
}
func (f *fakeIndex) Upsert(context.Context, string, model.RegisteredDomain) error { return nil }
func (f *fakeIndex) Delete(context.Context, string, string) error                 { return nil }
func (f *fakeIndex) ReplaceForServer(_ context.Context, serverID string, ds []model.RegisteredDomain) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.replaced == nil {
		f.replaced = map[string][]model.RegisteredDomain{}
	}
	f.replaced[serverID] = ds
	return nil
}

func (f *fakeIndex) synced(serverID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	ds, ok := f.replaced[serverID]
	return ok && len(ds) == 1 && ds[0].Name == "a.app"
}

func newTestDomainSync(t *testing.T, hub *fakeHub, retryDelay time.Duration) (*domainSync, *fakeIndex) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	idx := &fakeIndex{}
	s := newDomainSync(ctx, hub, idx, logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
	s.retryDelay = retryDelay
	return s, idx
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestDomainSyncRefiresOnHeartbeatAfterNotConnected is the distributed
// race: the hub publishes capabilities before the stream is active, so the
// first domain.list bounces; the agent's first heartbeat re-fires it.
func TestDomainSyncRefiresOnHeartbeatAfterNotConnected(t *testing.T) {
	hub := &fakeHub{}
	s, idx := newTestDomainSync(t, hub, time.Hour) // the retry timer never fires here
	s.online("srv-1")
	if hub.count() != 1 || idx.synced("srv-1") {
		t.Fatalf("first attempt: calls %d", hub.count())
	}
	hub.set(func(h *fakeHub) { h.connected = true })
	s.heartbeat("srv-1")
	if hub.count() != 2 || !idx.synced("srv-1") {
		t.Fatalf("heartbeat did not re-fire the pending sync: calls %d", hub.count())
	}
	s.heartbeat("srv-1")
	s.heartbeat("srv-1")
	if hub.count() != 2 {
		t.Fatalf("a settled sync re-fired: calls %d", hub.count())
	}
}

// TestDomainSyncRetriesOnceAfterNotConnected: without a heartbeat, the
// bounced sync is retried once after the delay.
func TestDomainSyncRetriesOnceAfterNotConnected(t *testing.T) {
	hub := &fakeHub{}
	s, idx := newTestDomainSync(t, hub, 10*time.Millisecond)
	s.online("srv-1")
	hub.set(func(h *fakeHub) { h.connected = true })
	waitFor(t, "the retry to sync", func() bool { return idx.synced("srv-1") })
	if hub.count() != 2 {
		t.Fatalf("calls = %d, want 2", hub.count())
	}
}

// TestDomainSyncIsBounded: an agent the hub keeps reporting as not
// connected is asked at most domainSyncMaxAttempts times per online period
// (one retry, then heartbeats); the next online period starts afresh.
func TestDomainSyncIsBounded(t *testing.T) {
	hub := &fakeHub{}
	s, _ := newTestDomainSync(t, hub, 10*time.Millisecond)
	s.online("srv-1")
	waitFor(t, "the retry", func() bool { return hub.count() == 2 })
	for i := 0; i < 5; i++ {
		s.heartbeat("srv-1")
	}
	time.Sleep(50 * time.Millisecond) // no second retry timer may fire
	if hub.count() != domainSyncMaxAttempts {
		t.Fatalf("calls = %d, want %d", hub.count(), domainSyncMaxAttempts)
	}
	s.online("srv-1")
	if hub.count() != domainSyncMaxAttempts+1 {
		t.Fatalf("a new online period did not sync: calls %d", hub.count())
	}
}

// TestDomainSyncSettlesOnce: standalone's in-process bridge answers the
// first attempt, so later heartbeats never sync again; a definitive agent
// error (no domain commands) settles it the same way.
func TestDomainSyncSettlesOnce(t *testing.T) {
	hub := &fakeHub{connected: true}
	s, idx := newTestDomainSync(t, hub, time.Millisecond)
	s.online("srv-1")
	s.heartbeat("srv-1")
	if hub.count() != 1 || !idx.synced("srv-1") {
		t.Fatalf("standalone: calls %d", hub.count())
	}

	hub.set(func(h *fakeHub) { h.agentErr = "unknown command type" })
	s.online("srv-2")
	s.heartbeat("srv-2")
	if hub.count() != 2 {
		t.Fatalf("an agent error re-fired: calls %d", hub.count())
	}
}

// TestDomainSyncInFlightAndPublishFailure: a heartbeat does not stack a
// second attempt on one in flight; a publish failure frees the slot so the
// next heartbeat retries.
func TestDomainSyncInFlightAndPublishFailure(t *testing.T) {
	hub := &fakeHub{connected: true, drop: true}
	s, _ := newTestDomainSync(t, hub, time.Hour)
	s.online("srv-1")
	s.heartbeat("srv-1")
	s.online("srv-1") // still in flight: kept, not restarted
	if hub.count() != 1 {
		t.Fatalf("stacked an attempt on one in flight: calls %d", hub.count())
	}

	hub.set(func(h *fakeHub) { h.drop, h.fail = false, true })
	s.online("srv-2")
	hub.set(func(h *fakeHub) { h.fail = false })
	s.heartbeat("srv-2")
	if hub.count() != 3 {
		t.Fatalf("publish failure not retried on heartbeat: calls %d", hub.count())
	}
	// Results for servers with nothing pending are ignored.
	s.result("srv-9", port.CommandResult{Success: true})
}
