package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"winterflow/internal/infra/transport/bus"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func testLog() *logger.Logger {
	return logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
}

func recv(t *testing.T, ch <-chan bus.Message) (bus.Message, bool) {
	t.Helper()
	select {
	case m := <-ch:
		return m, true
	case <-time.After(500 * time.Millisecond):
		return bus.Message{}, false
	}
}

func TestResponderRepliesOnHubChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := membus.NewBus(testLog())
	cfg := config.NewServerConfig("distributed")
	pemA, fpA := newCert(t, "srv-1")
	repo := fakeRepo{servers: map[string]bool{"srv-1": true}, cert: map[string]string{"srv-1": pemA}}
	if err := StartResponder(ctx, b, cfg, repo, testLog()); err != nil {
		t.Fatal(err)
	}
	replies, c, err := b.Subscribe(ctx, config.HubResponsesChannel("h1"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c() }()

	// An unknown type is ignored; the verify request is answered.
	_ = b.Publish(ctx, cfg.GetHubRequestsChannel(), bus.HubRequest{Type: "bogus", RequestID: "r0", HubID: "h1"})
	_ = b.Publish(ctx, cfg.GetHubRequestsChannel(), bus.HubRequest{
		Type: bus.HubRequestVerify, RequestID: "r1", HubID: "h1", ServerID: "srv-1", CertFingerprint: fpA,
	})
	m, ok := recv(t, replies)
	if !ok {
		t.Fatal("no reply")
	}
	var resp bus.HubResponse
	if err := json.Unmarshal([]byte(m.Payload), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.RequestID != "r1" || resp.Verdict != bus.VerdictAllowed {
		t.Fatalf("resp = %+v", resp)
	}

	// A DB error sends no reply.
	bad := fakeRepo{err: errors.New("db down")}
	b2 := membus.NewBus(testLog())
	if err := StartResponder(ctx, b2, cfg, bad, testLog()); err != nil {
		t.Fatal(err)
	}
	r2, c2, _ := b2.Subscribe(ctx, config.HubResponsesChannel("h1"))
	defer func() { _ = c2() }()
	_ = b2.Publish(ctx, cfg.GetHubRequestsChannel(), bus.HubRequest{Type: bus.HubRequestVerify, RequestID: "r2", HubID: "h1", ServerID: "x"})
	if m, ok := recv(t, r2); ok {
		t.Fatalf("unexpected reply %s", m.Payload)
	}
}

func TestRevokerPublishes(t *testing.T) {
	ctx := context.Background()
	b := membus.NewBus(testLog())
	cfg := config.NewServerConfig("distributed")
	ch, c, err := b.Subscribe(ctx, cfg.GetHubBroadcastChannel())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c() }()
	if err := NewBusRevoker(b, cfg).Revoke(ctx, "srv-9"); err != nil {
		t.Fatal(err)
	}
	m, ok := recv(t, ch)
	if !ok {
		t.Fatal("no broadcast")
	}
	var got bus.HubBroadcast
	if err := json.Unmarshal([]byte(m.Payload), &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != bus.HubBroadcastRevoked || got.ServerID != "srv-9" {
		t.Fatalf("got %+v", got)
	}
}
