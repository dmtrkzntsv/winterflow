package notification

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

func newManager() *Manager {
	return NewNotificationManager(logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"}))
}

// ref decodes the notification's ref from a queued message.
func ref(t *testing.T, raw string) string {
	t.Helper()
	var n model.Notification
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		t.Fatalf("payload = %q: %v", raw, err)
	}
	return n.Ref
}

func TestPublishReachesAllUserChannels(t *testing.T) {
	m := newManager()
	a1 := m.AddChannel("alice")
	a2 := m.AddChannel("alice")
	b := m.AddChannel("bob")

	m.Publish("alice", model.Notification{Type: model.NotificationOperationResult, Ref: "r1"})

	for _, ch := range []chan string{a1, a2} {
		select {
		case raw := <-ch:
			var n model.Notification
			if err := json.Unmarshal([]byte(raw), &n); err != nil || n.Ref != "r1" {
				t.Fatalf("payload = %q, err = %v", raw, err)
			}
		default:
			t.Fatal("alice channel did not receive the notification")
		}
	}
	select {
	case msg := <-b:
		t.Fatalf("bob must not receive alice's notification: %q", msg)
	default:
	}
}

func TestPublishToUnknownUserIsNoop(t *testing.T) {
	m := newManager()
	m.Publish("ghost", model.Notification{Ref: "r"}) // must not panic
}

// TestBurstIsDeliveredWithoutAReader: a status fan-out (apps_status, then
// its server_status transition) followed by an operation_result arrives
// while the SSE handler is still busy; every one of them must be queued.
// With a 1-slot buffer the operation_result was dropped and the UI waited
// for a result that never came.
func TestBurstIsDeliveredWithoutAReader(t *testing.T) {
	m := newManager()
	ch := m.AddChannel("alice")
	burst := []model.Notification{
		{Type: model.NotificationAppsStatus, Ref: "apps"},
		{Type: model.NotificationServerStatus, Ref: "server"},
		{Type: model.NotificationOperationResult, Ref: "result"},
	}
	for _, n := range burst {
		m.Publish("alice", n)
	}
	for _, want := range burst {
		select {
		case raw := <-ch:
			if got := ref(t, raw); got != want.Ref {
				t.Fatalf("got ref %q, want %q", got, want.Ref)
			}
		default:
			t.Fatalf("%s (%s) was dropped although nothing was reading yet", want.Type, want.Ref)
		}
	}
}

// TestFullBufferDropsWithoutBlocking: a stuck client (nobody reading) must
// cost Publish nothing: once its buffer is full, further notifications are
// dropped and Publish returns immediately, so other users are unaffected.
func TestFullBufferDropsWithoutBlocking(t *testing.T) {
	m := newManager()
	stuck := m.AddChannel("alice")
	other := m.AddChannel("bob")

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < streamBuffer+10; i++ {
			m.Publish("alice", model.Notification{Ref: fmt.Sprint(i)})
		}
		m.Publish("bob", model.Notification{Ref: "bob"})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a full stream")
	}

	if len(stuck) != streamBuffer {
		t.Fatalf("queued %d, want the buffer size %d", len(stuck), streamBuffer)
	}
	for i := 0; i < streamBuffer; i++ {
		if got := ref(t, <-stuck); got != fmt.Sprint(i) {
			t.Fatalf("message %d: ref %q (the oldest must be kept, the overflow dropped)", i, got)
		}
	}
	if got := ref(t, <-other); got != "bob" {
		t.Fatalf("bob got %q", got)
	}
}

func TestRemoveChannelClosesAndStopsDelivery(t *testing.T) {
	m := newManager()
	ch := m.AddChannel("alice")
	m.RemoveChannel("alice", ch)

	if _, open := <-ch; open {
		t.Fatal("removed channel should be closed")
	}
	m.Publish("alice", model.Notification{Ref: "r"}) // no panic on empty set
}
