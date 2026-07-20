package membus

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/logger"
)

func newBus(t *testing.T) *Bus {
	t.Helper()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	return NewBus(log)
}

func recv(t *testing.T, ch <-chan bus.Message) bus.Message {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
		return bus.Message{}
	}
}

func TestPublishSubscribeRoundTrip(t *testing.T) {
	b := newBus(t)
	ctx := context.Background()

	msgs, cancel, err := b.Subscribe(ctx, "q1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	if err := b.Publish(ctx, "q1", map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	m := recv(t, msgs)
	if m.Channel != "q1" {
		t.Fatalf("channel = %q", m.Channel)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(m.Payload), &body); err != nil || body["k"] != "v" {
		t.Fatalf("payload = %q, err %v", m.Payload, err)
	}
}

func TestChannelsAreIsolated(t *testing.T) {
	b := newBus(t)
	ctx := context.Background()

	q1, cancel1, _ := b.Subscribe(ctx, "q1")
	defer cancel1()
	q2, cancel2, _ := b.Subscribe(ctx, "q2")
	defer cancel2()

	_ = b.Publish(ctx, "q2", "only-q2")
	m := recv(t, q2)
	if m.Channel != "q2" {
		t.Fatalf("channel = %q", m.Channel)
	}
	select {
	case m := <-q1:
		t.Fatalf("q1 must not receive q2 traffic: %+v", m)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFanOutToMultipleSubscribers(t *testing.T) {
	b := newBus(t)
	ctx := context.Background()

	s1, c1, _ := b.Subscribe(ctx, "q")
	defer c1()
	s2, c2, _ := b.Subscribe(ctx, "q")
	defer c2()

	_ = b.Publish(ctx, "q", 42)
	if recv(t, s1).Payload != "42" || recv(t, s2).Payload != "42" {
		t.Fatal("both subscribers should receive the message")
	}
}

func TestCancelStopsDelivery(t *testing.T) {
	b := newBus(t)
	ctx := context.Background()

	msgs, cancel, _ := b.Subscribe(ctx, "q")
	if err := cancel(); err != nil {
		t.Fatal(err)
	}
	_ = b.Publish(ctx, "q", "late")
	select {
	case m, ok := <-msgs:
		if ok {
			t.Fatalf("received after cancel: %+v", m)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPublishUnmarshalableValueErrors(t *testing.T) {
	b := newBus(t)
	if err := b.Publish(context.Background(), "q", make(chan int)); err == nil {
		t.Fatal("expected marshal error for a channel value")
	}
}
