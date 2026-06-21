package dispatch

import (
	"context"
	"sync"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
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
	return NewManager(membus.NewBus(log), nm, config.NewServerConfig("distributed"), log), nm
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
