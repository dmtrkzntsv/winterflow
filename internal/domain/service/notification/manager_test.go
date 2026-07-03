package notification

import (
	"encoding/json"
	"testing"

	"winterflow/internal/domain/model"
)

func TestPublishReachesAllUserChannels(t *testing.T) {
	m := NewNotificationManager()
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
	m := NewNotificationManager()
	m.Publish("ghost", model.Notification{Ref: "r"}) // must not panic
}

func TestSlowChannelIsSkippedNotBlocked(t *testing.T) {
	m := NewNotificationManager()
	ch := m.AddChannel("alice") // buffer of 1
	m.Publish("alice", model.Notification{Ref: "first"})
	m.Publish("alice", model.Notification{Ref: "second"}) // buffer full: dropped, not blocking

	raw := <-ch
	var n model.Notification
	_ = json.Unmarshal([]byte(raw), &n)
	if n.Ref != "first" {
		t.Fatalf("got %q, want first", n.Ref)
	}
	select {
	case extra := <-ch:
		t.Fatalf("second publish should have been dropped, got %q", extra)
	default:
	}
}

func TestRemoveChannelClosesAndStopsDelivery(t *testing.T) {
	m := NewNotificationManager()
	ch := m.AddChannel("alice")
	m.RemoveChannel("alice", ch)

	if _, open := <-ch; open {
		t.Fatal("removed channel should be closed")
	}
	m.Publish("alice", model.Notification{Ref: "r"}) // no panic on empty set
}
