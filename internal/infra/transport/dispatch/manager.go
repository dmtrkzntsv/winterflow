// Package dispatch implements the fire-and-forward command path: it publishes
// commands to the request queue and routes agent results back to the
// originating user over SSE, replacing the old blocking reply.Manager.
//
// A command is published with a correlation id (request_id). The Manager
// records request_id → userID (with a TTL) so that when the agent's result
// arrives on the response queue, the bus subscriber can look up the owner and
// publish a notification to that user's SSE stream.
package dispatch

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/hubs"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

// pendingTTL bounds how long we keep a request_id → userID mapping waiting for
// a result. Generous: covers slow agent operations (e.g. image pulls).
const pendingTTL = 10 * time.Minute

type pending struct {
	userID   string
	expires  time.Time
	onResult func(port.CommandResult)
}

// Manager publishes commands and routes their results to users via the
// NotificationManager. One per API instance.
type Manager struct {
	bus    bus.Bus
	router hubs.Router
	nm     port.NotificationManager
	log    *logger.Logger

	mu      sync.Mutex
	pending map[string]pending // request_id → owner
}

// NewManager publishes each command to the queue router names for its
// server: the server's hub's queue (distributed) or the plain request queue
// (standalone).
func NewManager(b bus.Bus, router hubs.Router, nm port.NotificationManager, log *logger.Logger) *Manager {
	return &Manager{
		bus:     b,
		router:  router,
		nm:      nm,
		log:     log,
		pending: make(map[string]pending),
	}
}

// Dispatch publishes a command to its server's request queue and returns its
// request_id. It records request_id → userID so the eventual result can be
// routed back. A server the router cannot place (no hub) fails the dispatch
// before anything is recorded or published.
func (m *Manager) Dispatch(ctx context.Context, in port.DispatchInput) (string, error) {
	queue, err := m.router.QueueFor(ctx, in.AgentID)
	if err != nil {
		return "", err
	}
	requestID := util.GenerateID()

	payload, err := json.Marshal(in.Payload)
	if err != nil {
		return "", err
	}

	ttl := pendingTTL
	if in.Wait > ttl {
		ttl = in.Wait
	}
	m.remember(requestID, in.UserID, in.OnResult, ttl)

	if err := m.bus.Publish(ctx, queue, bus.CommandMessage{
		AgentID:   in.AgentID,
		RequestID: requestID,
		Type:      string(in.Type),
		Payload:   payload,
	}); err != nil {
		m.forget(requestID)
		return "", err
	}
	return requestID, nil
}

// HandleResult runs the request's OnResult hook (DB side effects) and then
// routes the notification to the originating user over SSE. Called by the bus
// response subscriber.
func (m *Manager) HandleResult(ntf model.Notification) {
	p, ok := m.take(ntf.Ref)
	if !ok {
		// Unknown/expired request id — nothing to deliver to.
		m.log.Debug("dispatch: no owner for result", "request_id", ntf.Ref)
		return
	}
	if p.onResult != nil {
		p.onResult(port.CommandResult{
			Success: ntf.Status == model.NotificationStatusSuccess,
			Error:   ntf.Error,
			Payload: rawPayload(ntf.Payload),
		})
	}
	m.nm.Publish(p.userID, ntf)
}

// rawPayload extracts the JSON bytes from a notification payload, which may be
// json.RawMessage, []byte, or a decoded value.
func rawPayload(v any) []byte {
	switch p := v.(type) {
	case nil:
		return nil
	case json.RawMessage:
		return p
	case []byte:
		return p
	default:
		b, _ := json.Marshal(v)
		return b
	}
}

func (m *Manager) remember(requestID, userID string, onResult func(port.CommandResult), ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evictExpiredLocked()
	m.pending[requestID] = pending{userID: userID, expires: time.Now().Add(ttl), onResult: onResult}
}

func (m *Manager) forget(requestID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pending, requestID)
}

func (m *Manager) take(requestID string) (pending, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pending[requestID]
	if !ok {
		return pending{}, false
	}
	delete(m.pending, requestID)
	if time.Now().After(p.expires) {
		return pending{}, false
	}
	return p, true
}

// evictExpiredLocked drops stale entries; caller holds the lock. Cheap because
// the map only holds in-flight requests.
func (m *Manager) evictExpiredLocked() {
	now := time.Now()
	for id, p := range m.pending {
		if now.After(p.expires) {
			delete(m.pending, id)
		}
	}
}

var _ port.CommandDispatcher = (*Manager)(nil)
