package hub

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/grpc/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeFlag is the drain flag a test flips.
type fakeFlag struct {
	on  atomic.Bool
	ids chan string
}

func (f *fakeFlag) IsDraining(_ context.Context, hubID string) (bool, error) {
	select {
	case f.ids <- hubID:
	default:
	}
	return f.on.Load(), nil
}

// moveStream records what the hub sends down a stream.
type moveStream struct {
	grpc.ServerStream
	mu  sync.Mutex
	got []*proto.ServerCommand
	at  []time.Time
}

func (s *moveStream) Send(c *proto.ServerCommand) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, c)
	s.at = append(s.at, time.Now())
	return nil
}

func (s *moveStream) Recv() (*proto.AgentMessage, error) { select {} }

func (s *moveStream) sent() ([]*proto.ServerCommand, []time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*proto.ServerCommand(nil), s.got...), append([]time.Time(nil), s.at...)
}

func waitAnnounced(t *testing.T, reg *recordingRegistry, draining bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-reg.announced:
			if got.Draining == draining {
				return
			}
		case <-deadline:
			t.Fatalf("no announcement with draining=%v", draining)
		}
	}
}

// Setting the flag shows in the hub's announcement after one check, well
// before the next periodic announcement; clearing it reverses that.
func TestDrainFlagSetsDraining(t *testing.T) {
	h := newTestHub(t, nil)
	h.drainCheckEvery = 20 * time.Millisecond
	flag := &fakeFlag{ids: make(chan string, 1)}
	reg := newRecordingRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	announced := h.StartAnnouncer(ctx, reg)
	watched := h.StartDrainWatch(ctx, flag)
	defer func() {
		cancel()
		<-announced
		<-watched
	}()

	waitAnnounced(t, reg, false)
	if id := <-flag.ids; id != "hub-01.winterflow.dev" {
		t.Fatalf("flag read for %q", id)
	}
	flag.on.Store(true)
	waitAnnounced(t, reg, true)
	if !h.status().Draining {
		t.Fatal("status not draining")
	}
	flag.on.Store(false)
	waitAnnounced(t, reg, false)
}

// 50 streams all get hub.move, at most about 20 per second.
func TestDrainSendsMoveRateLimited(t *testing.T) {
	h := newTestHub(t, nil)
	h.drainCheckEvery = 20 * time.Millisecond
	streams := make([]*moveStream, 50)
	for i := range streams {
		streams[i] = &moveStream{}
		h.agents[string(rune('A'+i))] = &agent{streamActive: true, stream: streams[i], lastSeen: time.Now()}
	}
	// Registered without a stream: nothing to send to.
	h.agents["idle"] = &agent{lastSeen: time.Now()}

	flag := &fakeFlag{ids: make(chan string, 1)}
	flag.on.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	watched := h.StartDrainWatch(ctx, flag)
	defer func() {
		cancel()
		<-watched
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		all := true
		for _, s := range streams {
			if got, _ := s.sent(); len(got) == 0 {
				all = false
				break
			}
		}
		if all {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not every stream got hub.move")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if elapsed := time.Since(start); elapsed < 2*time.Second {
		t.Fatalf("50 moves in %v, want >= 2s", elapsed)
	}
	// One more check cycle sends nothing new: each agent is told once.
	time.Sleep(100 * time.Millisecond)
	var times []time.Time
	for _, s := range streams {
		got, at := s.sent()
		if len(got) != 1 {
			t.Fatalf("stream got %d messages, want 1", len(got))
		}
		if typ := got[0].GetRequest().GetType(); typ != string(command.HubMove) {
			t.Fatalf("sent type %q", typ)
		}
		times = append(times, at...)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	for i := 20; i < len(times); i++ {
		if span := times[i].Sub(times[i-20]); span < 900*time.Millisecond {
			t.Fatalf("21 moves within %v", span)
		}
	}
}

func TestDrainingRefusesNewAgents(t *testing.T) {
	h := newTestHub(t, nil)
	req := &proto.RegisterAgentRequest{Base: &proto.BaseMessage{AgentId: "srv-1"}}

	h.drainFlag.Store(true)
	_, err := h.RegisterAgent(context.Background(), req)
	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "hub is draining" {
		t.Fatalf("draining: err = %v", err)
	}
	h.drainFlag.Store(false)
	h.stopping.Store(true)
	if _, err := h.RegisterAgent(context.Background(), req); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stopping: err = %v", err)
	}
	h.stopping.Store(false)
	// Accepting again: the request gets past the drain check (and fails on
	// the missing client certificate).
	if _, err := h.RegisterAgent(context.Background(), req); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("undrained: err = %v", err)
	}
}

func TestShutdownDrainsThenStops(t *testing.T) {
	t.Run("returns when the streams close", func(t *testing.T) {
		h := newTestHub(t, nil)
		h.srv = grpc.NewServer()
		s := &moveStream{}
		h.agents["srv-1"] = &agent{streamActive: true, stream: s, lastSeen: time.Now()}
		go func() {
			// The agent leaves once told to move.
			for {
				if got, _ := s.sent(); len(got) > 0 {
					h.agentsLock.Lock()
					delete(h.agents, "srv-1")
					h.agentsLock.Unlock()
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		start := time.Now()
		if err := h.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Fatalf("shutdown took %v", elapsed)
		}
		if !h.status().Draining {
			t.Fatal("a stopping hub must announce draining")
		}
	})
	t.Run("returns at the deadline otherwise", func(t *testing.T) {
		h := newTestHub(t, nil)
		h.srv = grpc.NewServer()
		h.agents["srv-1"] = &agent{streamActive: true, stream: &moveStream{}, lastSeen: time.Now()}
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		start := time.Now()
		_ = h.Shutdown(ctx)
		if elapsed := time.Since(start); elapsed < 350*time.Millisecond || elapsed > 3*time.Second {
			t.Fatalf("shutdown took %v, want about the deadline", elapsed)
		}
	})
}

// peerRegistry is a hub list a test edits; announcements are dropped.
type peerRegistry struct {
	mu   sync.Mutex
	hubs []model.Hub
	err  error
}

func (r *peerRegistry) set(hubs ...model.Hub) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hubs = hubs
}

func (r *peerRegistry) Announce(context.Context, model.Hub, time.Duration) error { return nil }
func (r *peerRegistry) Withdraw(context.Context, string) error                   { return nil }
func (r *peerRegistry) List(context.Context) ([]model.Hub, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]model.Hub(nil), r.hubs...), r.err
}

var (
	selfHub  = model.Hub{ID: "hub-01.winterflow.dev", Accepting: true, Draining: true}
	otherHub = model.Hub{ID: "hub-02.winterflow.dev", Accepting: true}
)

// shutdownWith runs Shutdown with reg as the hub's list, the agent srv-1 on
// a stream that never leaves, and returns how long it took and the stream.
// onStart, when set, runs in its own goroutine once the agent is in place.
func shutdownWith(t *testing.T, h *Hub, reg *peerRegistry, deadline time.Duration, onStart ...func(*moveStream)) (time.Duration, *moveStream) {
	t.Helper()
	h.srv = grpc.NewServer()
	s := &moveStream{}
	h.agents["srv-1"] = &agent{streamActive: true, stream: s, lastSeen: time.Now()}
	for _, f := range onStart {
		go f(s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	announced := h.StartAnnouncer(ctx, reg)
	defer func() {
		cancel()
		<-announced
	}()
	sctx, scancel := context.WithTimeout(context.Background(), deadline)
	defer scancel()
	start := time.Now()
	_ = h.Shutdown(sctx)
	return time.Since(start), s
}

// The only hub (or one whose peers are draining or not accepting) stops at
// once: there is nowhere to move its agents, so it tells them nothing.
func TestShutdownWithoutOtherHubDoesNotWait(t *testing.T) {
	for name, peers := range map[string][]model.Hub{
		"alone":             {selfHub},
		"peers unavailable": {selfHub, {ID: "hub-02", Accepting: true, Draining: true}, {ID: "hub-03"}},
		"empty list":        nil,
	} {
		t.Run(name, func(t *testing.T) {
			h := newTestHub(t, nil)
			reg := &peerRegistry{}
			reg.set(peers...)
			took, s := shutdownWith(t, h, reg, 10*time.Second)
			if took > time.Second {
				t.Fatalf("shutdown took %v", took)
			}
			if got, _ := s.sent(); len(got) != 0 {
				t.Fatalf("sent %d hub.move, want none", len(got))
			}
			if !h.status().Draining {
				t.Fatal("a stopping hub must announce draining")
			}
		})
	}
	t.Run("list fails", func(t *testing.T) {
		h := newTestHub(t, nil)
		reg := &peerRegistry{err: errors.New("redis down")}
		if took, _ := shutdownWith(t, h, reg, 10*time.Second); took > time.Second {
			t.Fatalf("shutdown took %v", took)
		}
	})
}

// No streams: nothing to wait for, even with another hub listed.
func TestShutdownWithoutStreamsReturnsAtOnce(t *testing.T) {
	h := newTestHub(t, nil)
	h.srv = grpc.NewServer()
	reg := &peerRegistry{}
	reg.set(selfHub, otherHub)
	ctx, cancel := context.WithCancel(context.Background())
	announced := h.StartAnnouncer(ctx, reg)
	defer func() {
		cancel()
		<-announced
	}()
	start := time.Now()
	if err := h.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("shutdown took %v", took)
	}
}

// With another hub listed the stopping hub tells its agents to move and
// waits for them (here up to the deadline: the agent stays).
func TestShutdownWithOtherHubDrains(t *testing.T) {
	h := newTestHub(t, nil)
	h.drainCheckEvery = 20 * time.Millisecond
	reg := &peerRegistry{}
	reg.set(selfHub, otherHub)
	took, s := shutdownWith(t, h, reg, 500*time.Millisecond)
	if took < 450*time.Millisecond || took > 3*time.Second {
		t.Fatalf("shutdown took %v, want about the deadline", took)
	}
	if got, _ := s.sent(); len(got) != 1 || got[0].GetRequest().GetType() != string(command.HubMove) {
		t.Fatalf("sent %v, want one hub.move", got)
	}
}

// The other hub leaves mid-drain: once every agent was told, the stopping
// hub stops waiting.
func TestShutdownStopsWaitingWhenTheOtherHubLeaves(t *testing.T) {
	h := newTestHub(t, nil)
	h.drainCheckEvery = 20 * time.Millisecond
	reg := &peerRegistry{}
	reg.set(selfHub, otherHub)
	took, s := shutdownWith(t, h, reg, 10*time.Second, func(s *moveStream) {
		for {
			if got, _ := s.sent(); len(got) > 0 {
				reg.set(selfHub)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	if took > 2*time.Second {
		t.Fatalf("shutdown took %v", took)
	}
	if got, _ := s.sent(); len(got) != 1 {
		t.Fatalf("sent %d hub.move, want 1", len(got))
	}
}

// An agent told to move that stays is told again after moveResend; an
// undrained hub stops telling and announces accepting again.
func TestDrainResendsThenUndrainStops(t *testing.T) {
	h := newTestHub(t, nil)
	h.drainCheckEvery = 20 * time.Millisecond
	h.moveResend = 300 * time.Millisecond
	s := &moveStream{}
	h.agents["srv-1"] = &agent{streamActive: true, stream: s, lastSeen: time.Now()}
	flag := &fakeFlag{ids: make(chan string, 1)}
	flag.on.Store(true)
	reg := newRecordingRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	announced := h.StartAnnouncer(ctx, reg)
	watched := h.StartDrainWatch(ctx, flag)
	defer func() {
		cancel()
		<-announced
		<-watched
	}()
	waitAnnounced(t, reg, true)

	deadline := time.Now().Add(5 * time.Second)
	for {
		if got, _ := s.sent(); len(got) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("hub.move not resent")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, at := s.sent()
	if gap := at[1].Sub(at[0]); gap < 300*time.Millisecond {
		t.Fatalf("resent after %v, want >= moveResend", gap)
	}

	flag.on.Store(false)
	waitAnnounced(t, reg, false)
	for stopAt := time.Now().Add(2 * time.Second); ; {
		h.drainMu.Lock()
		running := h.moverCancel != nil
		h.drainMu.Unlock()
		if !running {
			break
		}
		if time.Now().After(stopAt) {
			t.Fatal("mover still running after undrain")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// A send the mover had already started may still land; wait past it.
	time.Sleep(2 * moveEvery)
	n := len(func() []*proto.ServerCommand { g, _ := s.sent(); return g }())
	time.Sleep(3 * h.moveResend)
	if got, _ := s.sent(); len(got) != n {
		t.Fatalf("sent %d hub.move after undrain, want %d", len(got), n)
	}
}
