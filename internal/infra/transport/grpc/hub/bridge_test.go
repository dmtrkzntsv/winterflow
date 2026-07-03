package hub

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/bus"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// newTestHub builds a Hub with an in-process bus but no gRPC server, so we can
// exercise the bus bridge / correlation logic without TLS or a listener.
func newTestHub(t *testing.T, b bus.Bus) *Hub {
	t.Helper()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	return &Hub{
		agents: make(map[string]*agent),
		cfg:    config.NewServerConfig("distributed"),
		log:    log,
		bus:    b,
	}
}

// A command addressed to an agent that isn't connected must produce an error
// result on the response queue keyed by the original request id — so the caller
// wakes up immediately instead of blocking until timeout.
func TestDispatchToOfflineAgentPublishesError(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	b := membus.NewBus(log)
	h := newTestHub(t, b)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.NewServerConfig("distributed")
	results, cancelSub, err := b.Subscribe(ctx, cfg.GetBusResponseQueue())
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancelSub()

	if err := h.StartBusBridge(ctx); err != nil {
		t.Fatalf("StartBusBridge: %v", err)
	}

	// Give the bridge's subscriber a moment to register before publishing.
	time.Sleep(20 * time.Millisecond)

	if err := b.Publish(ctx, cfg.GetBusRequestQueue(), bus.CommandMessage{
		AgentID:   "missing-agent",
		RequestID: "req-42",
		Type:      string(command.TypeAppSave),
		Payload:   []byte(`{}`),
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case msg := <-results:
		var ntf model.Notification
		if err := json.Unmarshal([]byte(msg.Payload), &ntf); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if ntf.Ref != "req-42" {
			t.Errorf("result keyed by %q, want req-42", ntf.Ref)
		}
		if ntf.Status != model.NotificationStatusError {
			t.Errorf("status = %v, want error", ntf.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected an error result on the response queue, got none")
	}
}

// publishNotification must key the notification by request id and carry the
// payload through on success.
func TestPublishNotificationSuccess(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	b := membus.NewBus(log)
	h := newTestHub(t, b)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.NewServerConfig("distributed")
	results, cancelSub, err := b.Subscribe(ctx, cfg.GetBusResponseQueue())
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancelSub()
	time.Sleep(20 * time.Millisecond)

	h.publishNotification(model.Notification{
		Type:      model.NotificationOperationResult,
		Ref:       "req-7",
		Status:    model.NotificationStatusSuccess,
		Payload:   json.RawMessage(`{"app_id":"a1","revision":2}`),
		Timestamp: time.Now(),
	})

	select {
	case msg := <-results:
		var ntf model.Notification
		if err := json.Unmarshal([]byte(msg.Payload), &ntf); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if ntf.Ref != "req-7" || ntf.Status != model.NotificationStatusSuccess {
			t.Errorf("unexpected notification: %+v", ntf)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no result published")
	}
}
