package hub

import (
	"testing"
	"time"

	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/logger"
)

func TestReapOnceRemovesOnlyStaleAgents(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	h := newTestHub(t, membus.NewBus(log))

	now := time.Now()
	h.agents["fresh"] = &agent{lastSeen: now.Add(-10 * time.Second)}
	h.agents["stale"] = &agent{lastSeen: now.Add(-5 * time.Minute)}
	h.agents["edge"] = &agent{lastSeen: now.Add(-staleAgentTTL)} // exactly TTL: kept

	removed := h.reapOnce(now)
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, ok := h.agents["stale"]; ok {
		t.Error("stale agent should have been reaped")
	}
	if _, ok := h.agents["fresh"]; !ok {
		t.Error("fresh agent should remain")
	}
	if _, ok := h.agents["edge"]; !ok {
		t.Error("agent at exactly TTL should remain (strictly-greater removal)")
	}
}

func TestReapOnceCancelsStreamContext(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	h := newTestHub(t, membus.NewBus(log))

	canceled := false
	h.agents["stale"] = &agent{
		lastSeen:   time.Now().Add(-5 * time.Minute),
		cancelFunc: func() { canceled = true },
	}
	h.reapOnce(time.Now())
	if !canceled {
		t.Error("reaping a stale agent should cancel its stream context")
	}
}
