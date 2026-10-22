package dispatch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/hubs"
	"winterflow/internal/infra/transport/bus"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func portInput(agentID, userID string, typ command.Type, payload any) port.DispatchInput {
	return port.DispatchInput{AgentID: agentID, UserID: userID, Type: typ, Payload: payload}
}

// recordingNM captures Publish calls so we can assert routing.
type recordingNM struct {
	mu   sync.Mutex
	seen []struct {
		userID string
		ntf    model.Notification
	}
}

func (n *recordingNM) AddChannel(string) chan string     { return make(chan string, 1) }
func (n *recordingNM) RemoveChannel(string, chan string) {}
func (n *recordingNM) Publish(userID string, ntf model.Notification) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.seen = append(n.seen, struct {
		userID string
		ntf    model.Notification
	}{userID, ntf})
}

func newManager(t *testing.T) (*Manager, *recordingNM) {
	t.Helper()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	nm := &recordingNM{}
	return NewManager(membus.NewBus(log), hubs.SingleQueue(config.NewServerConfig("standalone")), nm, log), nm
}

// recordingBus records the channel of every publish.
type recordingBus struct {
	mu       sync.Mutex
	channels []string
	cmds     []bus.CommandMessage
}

func (b *recordingBus) Publish(_ context.Context, channel string, v any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.channels = append(b.channels, channel)
	if c, ok := v.(bus.CommandMessage); ok {
		b.cmds = append(b.cmds, c)
	}
	return nil
}

func (b *recordingBus) Subscribe(context.Context, ...string) (<-chan bus.Message, func() error, error) {
	return nil, nil, errors.New("not supported")
}

type serverHubs map[string]string

func (s serverHubs) ServerHub(_ context.Context, id string) (string, error) {
	hub, ok := s[id]
	if !ok {
		return "", model.ErrNotFound
	}
	return hub, nil
}

func TestManagerPublishesToServersHubQueue(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	b := &recordingBus{}
	servers := serverHubs{"srv-1": "hub-01.winterflow.dev", "srv-2": "hub-02.winterflow.dev", "srv-old": ""}
	m := NewManager(b, hubs.NewRouter(servers, config.NewServerConfig("distributed")), &recordingNM{}, log)

	for _, id := range []string{"srv-1", "srv-2"} {
		if _, err := m.Dispatch(context.Background(), portInput(id, "user-1", command.TypeAppSave, command.SaveAppRequest{})); err != nil {
			t.Fatalf("dispatch %s: %v", id, err)
		}
	}
	want := []string{"bus:requests:hub-01.winterflow.dev", "bus:requests:hub-02.winterflow.dev"}
	if len(b.channels) != 2 || b.channels[0] != want[0] || b.channels[1] != want[1] {
		t.Fatalf("channels = %v, want %v", b.channels, want)
	}
	if b.cmds[0].AgentID != "srv-1" {
		t.Fatalf("cmd = %+v", b.cmds[0])
	}

	// A server without a hub fails the dispatch (nothing published, nothing
	// left pending).
	_, err := m.Dispatch(context.Background(), portInput("srv-old", "user-1", command.TypeAppSave, command.SaveAppRequest{}))
	if !errors.Is(err, hubs.ErrServerHasNoHub) {
		t.Fatalf("no hub: want ErrServerHasNoHub, got %v", err)
	}
	if len(b.channels) != 2 {
		t.Fatalf("published for a server without a hub: %v", b.channels)
	}
	m.mu.Lock()
	pending := len(m.pending)
	m.mu.Unlock()
	if pending != 2 {
		t.Fatalf("pending = %d, want 2", pending)
	}
}

func TestDispatchReturnsRequestIDAndRoutesResult(t *testing.T) {
	m, nm := newManager(t)

	reqID, err := m.Dispatch(context.Background(), portInput("agent-1", "user-1", command.TypeAppSave, command.SaveAppRequest{}))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if reqID == "" {
		t.Fatal("expected a request id")
	}

	// Simulate the agent's result coming back on the response queue.
	m.HandleResult(model.Notification{Type: model.NotificationOperationResult, Ref: reqID, Status: model.NotificationStatusSuccess})

	nm.mu.Lock()
	defer nm.mu.Unlock()
	if len(nm.seen) != 1 {
		t.Fatalf("expected 1 routed notification, got %d", len(nm.seen))
	}
	if nm.seen[0].userID != "user-1" {
		t.Errorf("routed to %q, want user-1", nm.seen[0].userID)
	}
	if nm.seen[0].ntf.Ref != reqID {
		t.Errorf("notification ref = %q, want %q", nm.seen[0].ntf.Ref, reqID)
	}
}

func TestHandleResultUnknownRequestIsDropped(t *testing.T) {
	m, nm := newManager(t)
	m.HandleResult(model.Notification{Ref: "never-dispatched"})
	nm.mu.Lock()
	defer nm.mu.Unlock()
	if len(nm.seen) != 0 {
		t.Errorf("expected no routing for unknown request, got %d", len(nm.seen))
	}
}

func TestResultDeliveredOnlyOnce(t *testing.T) {
	m, nm := newManager(t)
	reqID, _ := m.Dispatch(context.Background(), portInput("a", "u", command.TypeAppSave, command.SaveAppRequest{}))
	m.HandleResult(model.Notification{Ref: reqID})
	m.HandleResult(model.Notification{Ref: reqID}) // second delivery must be dropped (entry consumed)
	nm.mu.Lock()
	defer nm.mu.Unlock()
	if len(nm.seen) != 1 {
		t.Errorf("expected exactly 1 delivery, got %d", len(nm.seen))
	}
}

// A command whose result comes when long work ends is awaited longer; a
// shorter Wait never cuts the default.
func TestDispatchWait(t *testing.T) {
	m, _ := newManager(t)
	long := portInput("agent-1", "user-1", command.TypeBackupRun, command.RunBackupRequest{})
	long.Wait = 2 * time.Hour
	short := portInput("agent-1", "user-1", command.TypeAppGet, command.GetAppRequest{})
	short.Wait = time.Second
	ids := map[string]time.Duration{}
	for in, want := range map[*port.DispatchInput]time.Duration{&long: 2 * time.Hour, &short: pendingTTL} {
		id, err := m.Dispatch(context.Background(), *in)
		if err != nil {
			t.Fatal(err)
		}
		ids[id] = want
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, want := range ids {
		left := time.Until(m.pending[id].expires)
		if left < want-time.Minute || left > want {
			t.Errorf("%s waits %s, want %s", id, left, want)
		}
	}
}
