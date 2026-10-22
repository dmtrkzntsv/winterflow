package hub

import (
	"context"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/usecase/hubs"
	"winterflow/internal/infra/transport/grpc/proto"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// DrainFlag reads a hub's drain flag (hub:draining:<hub_id> in Redis, set by
// winterflow-hub drain).
type DrainFlag interface {
	IsDraining(ctx context.Context, hubID string) (bool, error)
}

const (
	// DrainCheckEvery is how often a running hub reads its drain flag.
	DrainCheckEvery = 5 * time.Second
	// DrainTimeout bounds how long a stopping hub waits for its agents to
	// leave before it stops anyway (the unit's TimeoutStopSec=150 covers it).
	DrainTimeout = 2 * time.Minute
	// moveEvery paces hub.move messages, about 20 agents per second, so the
	// API is not flooded with "where to" requests.
	moveEvery = 50 * time.Millisecond
	// moveResendAfter is how long an agent told to move may stay before it
	// is told again (the API may have had no other hub at the time).
	moveResendAfter = time.Minute
	// drainPoll is how often Drain counts the streams left.
	drainPoll = 100 * time.Millisecond
)

// isDraining: the drain flag is set or the hub is stopping. A draining hub
// announces draining, moves its agents away and refuses new ones.
func (h *Hub) isDraining() bool {
	return h.drainFlag.Load() || h.stopping.Load()
}

// StartDrainWatch reads the hub's drain flag at start and every
// DrainCheckEvery until ctx ends; the returned channel closes after that.
func (h *Hub) StartDrainWatch(ctx context.Context, flag DrainFlag) <-chan struct{} {
	hubID := h.id
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.drainLoop(ctx, flag, hubID)
	}()
	return done
}

// drainLoop follows the drain flag: set, the hub announces draining and
// moves its agents; cleared, it announces accepting again. A failed read
// keeps the current state.
func (h *Hub) drainLoop(ctx context.Context, flag DrainFlag, hubID string) {
	every := h.checkEvery()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	defer h.stopMover()
	for {
		on, err := flag.IsDraining(ctx, hubID)
		switch {
		case err != nil:
			if ctx.Err() == nil {
				h.log.Warn("cannot read the hub's drain flag", "error", err)
			}
		case h.drainFlag.Swap(on) != on:
			if on {
				h.log.Info("hub draining: moving its agents to other hubs")
			} else {
				h.log.Info("hub undrained: accepting agents again")
			}
			h.drainChanged()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ErrNoOtherHub: a stopping hub's agents have nowhere to go (no other
// live hub accepts servers), so it does not wait for them.
var ErrNoOtherHub = errors.New("no other hub accepts agents")

// Drain is what stopping does first: the hub announces draining and
// refuses new agents. With no stream left it returns at once. When the hub
// list shows no other hub that accepts agents (or cannot be read) it moves
// nobody and returns ErrNoOtherHub: the API would have nowhere to send
// them, and waiting would only hold up the stop. Otherwise it tells every
// agent to move and waits until no stream is left (nil), ctx ends
// (ctx.Err()), or every agent was told and no other hub is listed any more
// (ErrNoOtherHub; checked every drain check interval).
func (h *Hub) Drain(ctx context.Context) error {
	h.stopping.Store(true)
	if h.activeStreams() == 0 {
		h.announceSoon()
		return nil
	}
	if !h.otherHubListed(ctx) {
		h.announceSoon()
		return ErrNoOtherHub
	}
	h.drainChanged()
	defer h.stopMover()
	ticker := time.NewTicker(drainPoll)
	defer ticker.Stop()
	nextPeerCheck := time.Now().Add(h.checkEvery())
	for {
		if h.activeStreams() == 0 {
			return nil
		}
		if now := time.Now(); !now.Before(nextPeerCheck) {
			nextPeerCheck = now.Add(h.checkEvery())
			if h.allTold() && !h.otherHubListed(ctx) {
				return ErrNoOtherHub
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// otherHubListed reports whether the hub list shows a hub other than this
// one that accepts agents and is not draining. Without a list (no
// announcer) it assumes one exists, so the hub drains as it would; a list
// that cannot be read counts as none (the API reads the same list to move
// an agent).
func (h *Hub) otherHubListed(ctx context.Context) bool {
	h.drainMu.Lock()
	reg := h.peers
	h.drainMu.Unlock()
	if reg == nil {
		return true
	}
	listed, err := reg.List(ctx)
	if err != nil {
		h.log.Warn("cannot read the hub list while stopping", "error", err)
		return false
	}
	self := h.id
	for _, hub := range listed {
		if hub.ID != self && hub.Accepting && !hub.Draining {
			return true
		}
	}
	return false
}

// allTold reports whether every active stream was told to move.
func (h *Hub) allTold() bool {
	h.agentsLock.RLock()
	defer h.agentsLock.RUnlock()
	for _, a := range h.agents {
		if a.streamActive && a.moveSentAt.IsZero() {
			return false
		}
	}
	return true
}

func (h *Hub) checkEvery() time.Duration {
	if h.drainCheckEvery > 0 {
		return h.drainCheckEvery
	}
	return DrainCheckEvery
}

func (h *Hub) resendAfter() time.Duration {
	if h.moveResend > 0 {
		return h.moveResend
	}
	return moveResendAfter
}

// drainChanged announces the new state at once and starts or stops moving
// agents to match it.
func (h *Hub) drainChanged() {
	h.announceSoon()
	if h.isDraining() {
		h.startMover()
	} else {
		h.stopMover()
	}
}

// announceSoon asks the announcer to announce the current state at once.
func (h *Hub) announceSoon() {
	select {
	case h.announceNow <- struct{}{}:
	default: // an announcement is already due, or no announcer runs
	}
}

func (h *Hub) startMover() {
	h.drainMu.Lock()
	defer h.drainMu.Unlock()
	if h.moverCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.moverCancel = cancel
	go h.moveAgents(ctx)
}

// stopMover stops moving agents. It does not wait: a send blocked on a
// stuck stream must not hold up a stop (moveTargets claims each agent under
// agentsLock, so a mover still finishing never doubles a newer one's sends).
func (h *Hub) stopMover() {
	h.drainMu.Lock()
	defer h.drainMu.Unlock()
	if h.moverCancel != nil {
		h.moverCancel()
		h.moverCancel = nil
	}
}

// moveTarget is one stream to tell to move.
type moveTarget struct {
	id     string
	a      *agent
	stream grpc.BidiStreamingServer[proto.AgentMessage, proto.ServerCommand]
}

// moveAgents sends hub.move down every active stream, one per moveEvery,
// then looks again every check interval for streams not yet told (or told
// over resendAfter ago), until ctx ends. It first lists the hub as draining
// itself, so an agent told to move never asks the API before the hub list
// shows it (the announcer's own write runs on another goroutine).
func (h *Hub) moveAgents(ctx context.Context) {
	h.announceDraining(ctx)
	every := h.checkEvery()
	for {
		if targets := h.moveTargets(time.Now()); len(targets) > 0 {
			pace := time.NewTicker(moveEvery)
			for _, t := range targets {
				select {
				case <-ctx.Done():
					pace.Stop()
					return
				case <-pace.C:
				}
				h.sendMove(t)
			}
			pace.Stop()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// announceDraining writes the hub's entry (draining) to the hub list the
// announcer uses, if one runs. A failure is logged: the announcer's own
// write, already due, may still land.
func (h *Hub) announceDraining(ctx context.Context) {
	h.drainMu.Lock()
	reg := h.peers
	h.drainMu.Unlock()
	if reg == nil {
		return
	}
	if err := reg.Announce(ctx, h.status(), hubs.AnnounceTTL); err != nil && ctx.Err() == nil {
		h.log.Warn("hub announce before moving agents failed", "error", err)
	}
}

// moveTargets claims the active streams due a hub.move.
func (h *Hub) moveTargets(now time.Time) []moveTarget {
	resend := h.resendAfter()
	h.agentsLock.Lock()
	defer h.agentsLock.Unlock()
	var out []moveTarget
	for id, a := range h.agents {
		if !a.streamActive || a.stream == nil {
			continue
		}
		if !a.moveSentAt.IsZero() && now.Sub(a.moveSentAt) < resend {
			continue
		}
		a.moveSentAt = now
		out = append(out, moveTarget{id: id, a: a, stream: a.stream})
	}
	return out
}

// sendMove writes one hub.move under the agent's send mutex (never while
// holding agentsLock). The agent asks the API where to go and sends no
// reply.
func (h *Hub) sendMove(t moveTarget) {
	id := fmt.Sprintf("hub-move-%d", time.Now().UnixNano())
	cmd := &proto.ServerCommand{Command: &proto.ServerCommand_Request{Request: &proto.RequestEnvelope{
		Base: &proto.BaseMessage{
			MessageId:       id,
			Timestamp:       timestamppb.Now(),
			AgentId:         t.id,
			ProtocolVersion: command.SchemaVersion,
		},
		RequestId:     id,
		Type:          string(command.HubMove),
		ContentType:   command.ContentTypeJSON,
		SchemaVersion: command.SchemaVersion,
		Payload:       []byte(`{}`),
	}}}
	if err := t.a.send(t.stream, cmd); err != nil {
		h.log.Debug("hub.move not delivered", "agent_id", t.id, "error", err)
		return
	}
	h.log.Debug("told agent to move", "agent_id", t.id)
}

// activeStreams counts the agents with a bound stream.
func (h *Hub) activeStreams() int {
	h.agentsLock.RLock()
	defer h.agentsLock.RUnlock()
	n := 0
	for _, a := range h.agents {
		if a.streamActive {
			n++
		}
	}
	return n
}
