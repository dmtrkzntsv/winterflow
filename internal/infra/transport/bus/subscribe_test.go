package bus_test

import (
	"context"
	"testing"
	"time"

	"winterflow/internal/infra/transport/bus"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/logger"
)

func TestSubscribeJSONDecodesAndSkipsMalformed(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	b := membus.NewBus(log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan bus.CommandMessage, 4)
	bus.SubscribeJSON(ctx, b, "q", log, func(cmd bus.CommandMessage) {
		got <- cmd
	})

	// Give the subscriber goroutine a beat to attach before publishing.
	time.Sleep(20 * time.Millisecond)

	if err := b.Publish(ctx, "q", bus.CommandMessage{RequestID: "r1", Type: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, "q", "not-a-command-message-shape"); err != nil {
		t.Fatal(err) // decodes into zero-value struct? no: string payload fails unmarshal into struct
	}
	if err := b.Publish(ctx, "q", bus.CommandMessage{RequestID: "r2", Type: "b"}); err != nil {
		t.Fatal(err)
	}

	want := []string{"r1", "r2"}
	for _, w := range want {
		select {
		case cmd := <-got:
			if cmd.RequestID != w {
				t.Fatalf("got request id %q, want %q", cmd.RequestID, w)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for message %q", w)
		}
	}
	select {
	case cmd := <-got:
		t.Fatalf("unexpected extra message: %+v", cmd)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSubscribeJSONStopsOnContextCancel(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	b := membus.NewBus(log)
	ctx, cancel := context.WithCancel(context.Background())

	got := make(chan bus.CommandMessage, 1)
	bus.SubscribeJSON(ctx, b, "q", log, func(cmd bus.CommandMessage) { got <- cmd })
	time.Sleep(20 * time.Millisecond)

	cancel()
	time.Sleep(20 * time.Millisecond)

	_ = b.Publish(context.Background(), "q", bus.CommandMessage{RequestID: "late"})
	select {
	case cmd := <-got:
		t.Fatalf("handler ran after cancel: %+v", cmd)
	case <-time.After(100 * time.Millisecond):
	}
}
