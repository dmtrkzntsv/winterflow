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
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

// pendingTTL bounds how long we keep a request_id → userID mapping waiting for
// a result. Generous: covers slow agent operations (e.g. image pulls).
const pendingTTL = 10 * time.Minute

type pending struct {
	userID  string
	expires time.Time
}

// Manager publishes commands and routes their results to users via the
// NotificationManager. One per API instance (region).
type Manager struct {
	bus bus.Bus
	nm  port.NotificationManager
	cfg *config.ServerConfig
	log *logger.Logger

	mu      sync.Mutex
	pending map[string]pending // request_id → owner
}

func NewManager(b bus.Bus, nm port.NotificationManager, cfg *config.ServerConfig, log *logger.Logger) *Manager {
	return &Manager{
		bus:     b,
		nm:      nm,
		cfg:     cfg,
		log:     log,
		pending: make(map[string]pending),
	}
}

// Dispatch publishes a command to the request queue and returns its request_id.
// It records request_id → userID so the eventual result can be routed back.
func (m *Manager) Dispatch(ctx context.Context, in port.DispatchInput) (string, error) {
	requestID := util.GenerateID()

	payload, err := json.Marshal(in.Payload)
	if err != nil {
		return "", err
	}

	m.remember(requestID, in.UserID)

	if err := m.bus.Publish(ctx, m.cfg.GetBusRequestQueue(), bus.CommandMessage{
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

// HandleResult routes a result notification (from the response queue) to the
// user who originated the request. Called by the bus response subscriber.
func (m *Manager) HandleResult(ntf model.Notification) {
	userID, ok := m.take(ntf.Ref)
	if !ok {
		// Unknown/expired request id — nothing to deliver to.
		m.log.Debug("dispatch: no owner for result", "request_id", ntf.Ref)
		return
	}
	m.nm.Publish(userID, ntf)
}

func (m *Manager) remember(requestID, userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evictExpiredLocked()
	m.pending[requestID] = pending{userID: userID, expires: time.Now().Add(pendingTTL)}
}

func (m *Manager) forget(requestID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pending, requestID)
}

func (m *Manager) take(requestID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pending[requestID]
	if !ok {
		return "", false
	}
	delete(m.pending, requestID)
	if time.Now().After(p.expires) {
		return "", false
	}
	return p.userID, true
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
