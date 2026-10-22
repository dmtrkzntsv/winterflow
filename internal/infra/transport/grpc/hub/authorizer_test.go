package hub

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"winterflow/internal/infra/transport/bus"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func newTestAuthorizer(t *testing.T, timeout time.Duration) (*BusAuthorizer, bus.Bus, *config.ServerConfig, context.Context) {
	t.Helper()
	t.Setenv("HUB_PUBLIC_ADDR", "hub-01.winterflow.dev:50051")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	b := membus.NewBus(log)
	cfg := config.NewServerConfig("distributed")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a := NewBusAuthorizer(b, cfg, cfg.GetHubID(), timeout, log)
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return a, b, cfg, ctx
}

// respond answers every hub request with the given verdicts in order, each
// after delay.
func respond(t *testing.T, ctx context.Context, b bus.Bus, cfg *config.ServerConfig, delays []time.Duration, verdicts []bus.AgentVerdict) <-chan bus.HubRequest {
	t.Helper()
	msgs, cancel, err := b.Subscribe(ctx, cfg.GetHubRequestsChannel())
	if err != nil {
		t.Fatal(err)
	}
	seen := make(chan bus.HubRequest, 4)
	go func() {
		defer func() { _ = cancel() }()
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-msgs:
				var req bus.HubRequest
				if err := json.Unmarshal([]byte(m.Payload), &req); err != nil {
					continue
				}
				seen <- req
				for i, v := range verdicts {
					time.Sleep(delays[i])
					_ = b.Publish(ctx, config.HubResponsesChannel(req.HubID), bus.HubResponse{RequestID: req.RequestID, Verdict: v, Reason: string(v)})
				}
			}
		}
	}()
	return seen
}

func pendingLen(a *BusAuthorizer) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.pending)
}

func TestAuthorizerAllowed(t *testing.T) {
	a, b, cfg, ctx := newTestAuthorizer(t, 2*time.Second)
	seen := respond(t, ctx, b, cfg, []time.Duration{0}, []bus.AgentVerdict{bus.VerdictAllowed})

	verdict, _, err := a.Authorize(ctx, "srv-1", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if verdict != bus.VerdictAllowed {
		t.Fatalf("verdict = %q", verdict)
	}
	req := <-seen
	if req.Type != bus.HubRequestVerify || req.HubID != "hub-01.winterflow.dev" || req.ServerID != "srv-1" || req.CertFingerprint != "abc" || req.RequestID == "" {
		t.Fatalf("request = %+v", req)
	}
	if n := pendingLen(a); n != 0 {
		t.Fatalf("pending = %d", n)
	}
}

func TestAuthorizerTimeout(t *testing.T) {
	a, _, _, ctx := newTestAuthorizer(t, 100*time.Millisecond)
	start := time.Now()
	if _, _, err := a.Authorize(ctx, "srv-1", "abc"); err == nil {
		t.Fatal("want timeout error")
	}
	if d := time.Since(start); d < 100*time.Millisecond || d > 2*time.Second {
		t.Fatalf("returned after %v", d)
	}
	if n := pendingLen(a); n != 0 {
		t.Fatalf("pending = %d", n)
	}
}

func TestAuthorizerContextCancelled(t *testing.T) {
	a, _, _, ctx := newTestAuthorizer(t, 5*time.Second)
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, _, err := a.Authorize(cctx, "srv-1", "abc"); err == nil {
		t.Fatal("want error")
	}
	if n := pendingLen(a); n != 0 {
		t.Fatalf("pending = %d", n)
	}
}

func TestAuthorizerLateAndDuplicateReplies(t *testing.T) {
	a, b, cfg, ctx := newTestAuthorizer(t, 200*time.Millisecond)
	// First reply arrives at once; a duplicate with another verdict arrives
	// after the wait has ended.
	respond(t, ctx, b, cfg, []time.Duration{0, 400 * time.Millisecond}, []bus.AgentVerdict{bus.VerdictRevoked, bus.VerdictAllowed})

	verdict, reason, err := a.Authorize(ctx, "srv-1", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if verdict != bus.VerdictRevoked || reason != "revoked" {
		t.Fatalf("verdict = %q, %q", verdict, reason)
	}
	time.Sleep(600 * time.Millisecond) // the late duplicate is dropped
	if n := pendingLen(a); n != 0 {
		t.Fatalf("pending = %d", n)
	}
	// A reply for an id nobody waits for is dropped too.
	_ = b.Publish(ctx, config.HubResponsesChannel(cfg.GetHubID()), bus.HubResponse{RequestID: "nobody", Verdict: bus.VerdictAllowed})
	time.Sleep(50 * time.Millisecond)
	if n := pendingLen(a); n != 0 {
		t.Fatalf("pending = %d", n)
	}
}

func TestAuthorizerStartTwice(t *testing.T) {
	a, _, _, ctx := newTestAuthorizer(t, time.Second)
	if err := a.Start(ctx); err == nil {
		t.Fatal("second Start should fail")
	}
}
