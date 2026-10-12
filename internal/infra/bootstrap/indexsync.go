package bootstrap

import (
	"context"
	"sync"
	"time"

	"winterflow/internal/domain/port"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/logger"
)

const (
	// indexSyncRetryDelay is the pause before the one retry of a sync the
	// hub bounced because the agent's stream was not active yet.
	indexSyncRetryDelay = 3 * time.Second
	// indexSyncTimeout bounds one attempt: past it an attempt whose result
	// never arrived no longer blocks the next one.
	indexSyncTimeout = time.Minute
	// indexSyncMaxAttempts caps the attempts per online period (the first
	// dispatch, its retry, re-fires on heartbeats), so a server the hub keeps
	// reporting as not connected is not asked again on every heartbeat.
	indexSyncMaxAttempts = 3
	// indexSyncPublishTimeout bounds the publish of one list command.
	indexSyncPublishTimeout = 10 * time.Second
)

// indexSync fills one of a server's API indexes (server_domains,
// server_storages) when its agent comes online: a list command dispatched
// for no user (the result reaches no SSE stream) whose result replaces the
// index rows, unchanged rows costing no write.
//
// In the distributed topology the hub publishes the capabilities event from
// RegisterAgent, before the agent's stream is active, so the first attempt
// usually bounces with bus.ErrAgentNotConnected. A sync therefore stays pending
// until a result arrives: a not-connected result is retried once after a
// short delay, and the agent's next heartbeat (EventServerOnline, sent as
// soon as the stream is up) re-fires a pending sync, up to
// indexSyncMaxAttempts. Any other result clears it — an agent without the
// list command answers an error that a retry would not change.
// Standalone's in-process bridge answers the first attempt, so it syncs once.
type indexSync struct {
	what       string // what is synced, for logs: "registered domains"
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

// newIndexSync builds a sync whose list is an app usecase method (built on
// deps) dispatching a list command for no user; a dispatcher tap observes
// each result.
func newIndexSync(ctx context.Context, what string, d port.CommandDispatcher, deps appuc.Deps,
	list func(uc *appuc.UseCase, ctx context.Context, userID, serverID string) (string, error), log *logger.Logger) *indexSync {
	s := &indexSync{what: what, ctx: ctx, retryDelay: indexSyncRetryDelay, log: log, pending: map[string]*syncState{}}
	deps.CommandDispatcher, deps.Log = resultTap{d, s.result}, log
	uc := appuc.NewUseCase(&deps)
	s.list = func(ctx context.Context, serverID string) error {
		_, err := list(uc, ctx, "", serverID)
		return err
	}
	return s
}

// newDomainSync fills server_domains through ListServerDomains.
func newDomainSync(ctx context.Context, d port.CommandDispatcher, repo port.ServerDomainRepository, log *logger.Logger) *indexSync {
	return newIndexSync(ctx, "registered domains", d, appuc.Deps{ServerDomainRepository: repo}, (*appuc.UseCase).ListServerDomains, log)
}

// newStorageSync fills server_storages through ListServerStorages.
func newStorageSync(ctx context.Context, d port.CommandDispatcher, repo port.ServerStorageRepository, log *logger.Logger) *indexSync {
	return newIndexSync(ctx, "storages", d, appuc.Deps{ServerStorageRepository: repo}, (*appuc.UseCase).ListServerStorages, log)
}

// indexSyncs drives every index sync from the same server events.
type indexSyncs []serverSync

func (all indexSyncs) online(serverID string) {
	for _, s := range all {
		s.online(serverID)
	}
}

func (all indexSyncs) heartbeat(serverID string) {
	for _, s := range all {
		s.heartbeat(serverID)
	}
}

// online starts a fresh sync for a server that came online or (re)registered.
func (s *indexSync) online(serverID string) {
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
func (s *indexSync) heartbeat(serverID string) {
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
func (s *indexSync) claimLocked(st *syncState) bool {
	now := time.Now()
	if now.Before(st.inFlightUntil) || st.attempts >= indexSyncMaxAttempts {
		return false
	}
	st.attempts++
	st.inFlightUntil = now.Add(indexSyncTimeout)
	return true
}

func (s *indexSync) dispatch(serverID string) {
	ctx, cancel := context.WithTimeout(s.ctx, indexSyncPublishTimeout)
	defer cancel()
	if err := s.list(ctx, serverID); err != nil {
		s.log.Warn("sync "+s.what+": dispatch the list", "error", err, "server_id", serverID)
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
func (s *indexSync) result(serverID string, res port.CommandResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.pending[serverID]
	if !ok {
		return
	}
	if res.Success || res.Error != bus.ErrAgentNotConnected {
		if !res.Success {
			s.log.Debug("sync "+s.what+": agent answered an error", "error", res.Error, "server_id", serverID)
		}
		delete(s.pending, serverID)
		return
	}
	st.inFlightUntil = time.Time{}
	if st.attempts >= indexSyncMaxAttempts {
		s.log.Warn("sync "+s.what+": agent not connected, giving up until it comes online again", "server_id", serverID)
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
