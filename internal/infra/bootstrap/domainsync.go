package bootstrap

import (
	"context"
	"strings"
	"sync"
	"time"

	"winterflow/internal/domain/port"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/pkg/logger"
)

const (
	// domainSyncRetryDelay is the pause before the one retry of a sync the
	// hub bounced because the agent's stream was not active yet.
	domainSyncRetryDelay = 3 * time.Second
	// domainSyncTimeout bounds one attempt: past it an attempt whose result
	// never arrived no longer blocks the next one.
	domainSyncTimeout = time.Minute
	// domainSyncMaxAttempts caps the attempts per online period (the first
	// dispatch, its retry, re-fires on heartbeats), so a server the hub keeps
	// reporting as not connected is not asked again on every heartbeat.
	domainSyncMaxAttempts = 3
	// domainSyncPublishTimeout bounds the publish of one domain.list.
	domainSyncPublishTimeout = 10 * time.Second
)

// notConnected reports whether a command failed because the hub had no
// active stream for the agent (hub.dispatchToAgent's "agent not connected").
func notConnected(errMsg string) bool { return strings.Contains(errMsg, "not connected") }

// domainSync fills a server's server_domains index when its agent comes
// online: a domain.list dispatched for no user (the result reaches no SSE
// stream) whose result replaces the index rows, unchanged rows costing no
// write.
//
// In the distributed topology the hub publishes the capabilities event from
// RegisterAgent, before the agent's stream is active, so the first attempt
// usually bounces with "agent not connected". A sync therefore stays pending
// until a result arrives: a not-connected result is retried once after a
// short delay, and the agent's next heartbeat (EventServerOnline, sent as
// soon as the stream is up) re-fires a pending sync, up to
// domainSyncMaxAttempts. Any other result clears it — an agent without
// domain commands answers an error that a retry would not change.
// Standalone's in-process bridge answers the first attempt, so it syncs once.
type domainSync struct {
	ctx        context.Context
	list       func(ctx context.Context, serverID string) error
	retryDelay time.Duration
	log        *logger.Logger

	mu      sync.Mutex
	pending map[string]*syncState
}

type syncState struct {
	attempts      int
	retried       bool
	inFlightUntil time.Time
}

// newDomainSync wires the sync through the app usecase's ListServerDomains,
// observing each result via a dispatcher tap.
func newDomainSync(ctx context.Context, d port.CommandDispatcher, repo port.ServerDomainRepository, log *logger.Logger) *domainSync {
	s := &domainSync{ctx: ctx, retryDelay: domainSyncRetryDelay, log: log, pending: map[string]*syncState{}}
	uc := appuc.NewUseCase(&appuc.Deps{CommandDispatcher: resultTap{d, s.result}, ServerDomainRepository: repo, Log: log})
	s.list = func(ctx context.Context, serverID string) error {
		_, err := uc.ListServerDomains(ctx, "", serverID)
		return err
	}
	return s
}

// online starts a fresh sync for a server that came online or (re)registered.
func (s *domainSync) online(serverID string) {
	s.mu.Lock()
	st, ok := s.pending[serverID]
	if !ok || time.Now().After(st.inFlightUntil) {
		st = &syncState{}
		s.pending[serverID] = st
	}
	fire := s.claimLocked(st)
	s.mu.Unlock()
	if fire {
		s.dispatch(serverID)
	}
}

// heartbeat re-fires a pending sync: the agent's stream is active now.
func (s *domainSync) heartbeat(serverID string) {
	s.mu.Lock()
	st, ok := s.pending[serverID]
	fire := ok && s.claimLocked(st)
	s.mu.Unlock()
	if fire {
		s.dispatch(serverID)
	}
}

// claimLocked reserves an attempt unless one is in flight or the budget is
// spent.
func (s *domainSync) claimLocked(st *syncState) bool {
	now := time.Now()
	if now.Before(st.inFlightUntil) || st.attempts >= domainSyncMaxAttempts {
		return false
	}
	st.attempts++
	st.inFlightUntil = now.Add(domainSyncTimeout)
	return true
}

func (s *domainSync) dispatch(serverID string) {
	ctx, cancel := context.WithTimeout(s.ctx, domainSyncPublishTimeout)
	defer cancel()
	if err := s.list(ctx, serverID); err != nil {
		s.log.Warn("sync registered domains: dispatch domain.list", "error", err, "server_id", serverID)
		s.mu.Lock()
		if st, ok := s.pending[serverID]; ok {
			st.inFlightUntil = time.Time{}
		}
		s.mu.Unlock()
	}
}

// result settles an attempt: anything but not-connected clears the pending
// sync; not-connected keeps it for the next heartbeat and schedules the one
// retry.
func (s *domainSync) result(serverID string, res port.CommandResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.pending[serverID]
	if !ok {
		return
	}
	if res.Success || !notConnected(res.Error) {
		if !res.Success {
			s.log.Debug("sync registered domains: agent answered an error", "error", res.Error, "server_id", serverID)
		}
		delete(s.pending, serverID)
		return
	}
	st.inFlightUntil = time.Time{}
	if st.attempts >= domainSyncMaxAttempts {
		s.log.Warn("sync registered domains: agent not connected, giving up until it comes online again", "server_id", serverID)
		delete(s.pending, serverID)
		return
	}
	if !st.retried {
		st.retried = true
		time.AfterFunc(s.retryDelay, func() {
			if s.ctx.Err() == nil {
				s.heartbeat(serverID)
			}
		})
	}
}

// resultTap is a dispatcher that reports every command's result, after the
// command's own OnResult, to tap.
type resultTap struct {
	port.CommandDispatcher
	tap func(serverID string, res port.CommandResult)
}

func (d resultTap) Dispatch(ctx context.Context, in port.DispatchInput) (string, error) {
	own, serverID := in.OnResult, in.AgentID
	in.OnResult = func(res port.CommandResult) {
		if own != nil {
			own(res)
		}
		d.tap(serverID, res)
	}
	return d.CommandDispatcher.Dispatch(ctx, in)
}
