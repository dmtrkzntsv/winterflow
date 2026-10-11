package bootstrap

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/logger"
)

type fakeStore struct{}

func (fakeStore) TouchLastSeen(context.Context, string) error { return nil }
func (fakeStore) SaveCapabilities(context.Context, string, map[string]string, map[string]bool) error {
	return nil
}

// recorder captures fan-out publishes keyed by user.
type recorder struct {
	mu   sync.Mutex
	sent []model.Notification
	to   []string
}

func (r *recorder) publish(userID string, n model.Notification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.to = append(r.to, userID)
	r.sent = append(r.sent, n)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sent)
}

func newTestSink(rec *recorder, users []string) (*eventSink, *status.Cache) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cache := status.NewCache(90 * time.Second)
	fan := &statusFanout{
		userIDs: func(context.Context, string) ([]string, error) { return users, nil },
		publish: rec.publish,
		log:     log,
	}
	return &eventSink{cache: cache, store: fakeStore{}, fan: fan, log: log}, cache
}

func TestServerOnlineTransitionFansOutOnce(t *testing.T) {
	rec := &recorder{}
	sink, _ := newTestSink(rec, []string{"alice", "bob"})
	ctx := context.Background()

	ev := bus.EventMessage{ServerID: "srv-1", Kind: bus.EventServerOnline}
	sink.handle(ctx, ev) // unknown -> online: fan out to both users
	sink.handle(ctx, ev) // still online: no fan-out

	if got := rec.count(); got != 2 {
		t.Fatalf("want 2 notifications (one per user, single transition), got %d", got)
	}
	n := rec.sent[0]
	if n.Type != model.NotificationServerStatus {
		t.Fatalf("type = %q", n.Type)
	}
	p, ok := n.Payload.(model.ServerStatusPayload)
	if !ok || p.ServerID != "srv-1" || p.Liveness != string(status.LivenessOnline) {
		t.Fatalf("payload = %#v", n.Payload)
	}
}

func TestAppsStatusEventFansOutAppsAndLiveness(t *testing.T) {
	rec := &recorder{}
	sink, _ := newTestSink(rec, []string{"alice"})
	ctx := context.Background()

	body, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{
		{AppID: "app-1", StatusCode: command.ContainerStatusActive},
	}})
	sink.handle(ctx, bus.EventMessage{ServerID: "srv-1", Kind: bus.EventAppsStatus, Payload: body})

	// First report is both an apps_status and a liveness transition.
	if got := rec.count(); got != 2 {
		t.Fatalf("want apps_status + server_status, got %d: %+v", got, rec.sent)
	}
	var kinds []model.NotificationType
	for _, n := range rec.sent {
		kinds = append(kinds, n.Type)
	}
	seen := map[model.NotificationType]bool{}
	for _, k := range kinds {
		seen[k] = true
	}
	if !seen[model.NotificationAppsStatus] || !seen[model.NotificationServerStatus] {
		t.Fatalf("kinds = %v", kinds)
	}

	for _, n := range rec.sent {
		if n.Type != model.NotificationAppsStatus {
			continue
		}
		p, ok := n.Payload.(command.AppsStatusPayload)
		if !ok || p.ServerID != "srv-1" || len(p.Apps) != 1 || p.Apps[0].AppID != "app-1" {
			t.Fatalf("apps payload = %#v", n.Payload)
		}
	}
}

func TestSweepFansOutUnknownTransition(t *testing.T) {
	rec := &recorder{}
	sink, cache := newTestSink(rec, []string{"alice"})
	ctx := context.Background()

	sink.handle(ctx, bus.EventMessage{ServerID: "srv-1", Kind: bus.EventServerOnline})
	if rec.count() != 1 {
		t.Fatalf("setup: want 1 online notification, got %d", rec.count())
	}

	// Simulate the sweeper firing after the TTL.
	sink.sweep(ctx, cache, time.Now().Add(5*time.Minute))

	if got := rec.count(); got != 2 {
		t.Fatalf("want an unknown transition after sweep, got %d total", got)
	}
	last := rec.sent[len(rec.sent)-1]
	p, ok := last.Payload.(model.ServerStatusPayload)
	if !ok || p.Liveness != string(status.LivenessUnknown) {
		t.Fatalf("sweep payload = %#v", last.Payload)
	}
}

// fakeSync records the sink's calls into the per-server index sync.
type fakeSync struct{ started, beats []string }

func (f *fakeSync) online(serverID string)    { f.started = append(f.started, serverID) }
func (f *fakeSync) heartbeat(serverID string) { f.beats = append(f.beats, serverID) }

// TestOnlineSyncRunsWhenAServerComesOnline: the per-server index sync starts
// on an unknown->online transition (whichever event causes it) and on every
// capabilities event (the agent registered); a steady heartbeat only nudges
// it (re-fires it while still pending).
func TestOnlineSyncRunsWhenAServerComesOnline(t *testing.T) {
	sink, cache := newTestSink(&recorder{}, nil)
	synced := &fakeSync{}
	sink.sync = synced
	ctx := context.Background()
	beat := bus.EventMessage{ServerID: "srv-1", Kind: bus.EventServerOnline}

	sink.handle(ctx, beat) // transition
	sink.handle(ctx, beat) // steady
	caps, _ := json.Marshal(capabilitiesEvent{Capabilities: map[string]string{"os": "linux"}})
	sink.handle(ctx, bus.EventMessage{ServerID: "srv-1", Kind: bus.EventCapabilities, Payload: caps}) // re-registered
	status, _ := json.Marshal(command.GetAppsStatusResponse{})
	sink.handle(ctx, bus.EventMessage{ServerID: "srv-2", Kind: bus.EventAppsStatus, Payload: status})        // transition
	sink.handle(ctx, bus.EventMessage{ServerID: "srv-2", Kind: bus.EventAppsStatus, Payload: status})        // steady
	sink.handle(ctx, bus.EventMessage{ServerID: "srv-3", Kind: bus.EventCapabilities, Payload: []byte("{")}) // undecodable: no sync

	if want := []string{"srv-1", "srv-1", "srv-2"}; strings.Join(synced.started, ",") != strings.Join(want, ",") {
		t.Fatalf("online = %v, want %v", synced.started, want)
	}
	if want := []string{"srv-1"}; strings.Join(synced.beats, ",") != strings.Join(want, ",") {
		t.Fatalf("heartbeats = %v, want %v", synced.beats, want)
	}

	// After the server went quiet, its next heartbeat starts a sync again.
	sink.sweep(ctx, cache, time.Now().Add(5*time.Minute))
	sink.handle(ctx, beat)
	if len(synced.started) != 4 {
		t.Fatalf("online = %v, want a sync after coming back", synced.started)
	}
}
