package bootstrap

import (
	"context"
	"sync"

	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/sharedvars"
	"winterflow/pkg/logger"
)

// varsSync keeps every agent's shared-variables snapshot current without a
// polling timer:
//   - an agent coming online (or re-registering) gets a vars.sync, with the
//     same pending/retry machinery as the registered-domains fill;
//   - an apps.status report whose vars_revision is behind
//     servers.vars_revision is pushed again, once per revision, healing a
//     missed push within one report (an agent ahead of the database has the
//     revision raised past its own first, see ObserveRevision);
//   - a changed public key purges the server's ciphertexts before the new
//     capabilities are stored, and the purged snapshot is pushed;
//   - agent-reported reload outcomes fan out to the org.
type varsSync struct {
	*indexSync
	uc  *sharedvars.UseCase
	log *logger.Logger

	mu     sync.Mutex
	pushed map[string]int64 // server -> revision last drift-pushed
}

func newVarsSync(ctx context.Context, d port.CommandDispatcher, deps sharedvars.Deps, log *logger.Logger) *varsSync {
	online := &indexSync{what: "shared variables", ctx: ctx, retryDelay: indexSyncRetryDelay, log: log, pending: map[string]*syncState{}}
	deps.Dispatcher = resultTap{d, online.result}
	if deps.Log == nil {
		deps.Log = log
	}
	s := &varsSync{indexSync: online, uc: sharedvars.New(&deps), log: log, pushed: map[string]int64{}}
	online.list = func(ctx context.Context, serverID string) error { return s.uc.Sync(ctx, serverID, nil) }
	return s
}

// capabilities runs before the event's capabilities are stored, so the
// stored key is still the previous one. After a purge the server's sync
// starts over: an attempt already in flight carries the old snapshot, and
// its result no longer settles anything.
func (s *varsSync) capabilities(ctx context.Context, serverID string, caps map[string]string) {
	if !s.uc.KeyObserved(ctx, serverID, caps) {
		return
	}
	s.indexSync.mu.Lock()
	delete(s.pending, serverID)
	s.indexSync.mu.Unlock()
	s.online(serverID)
}

// revision handles the vars_revision of an apps.status report.
func (s *varsSync) revision(ctx context.Context, serverID string, agentRev int64) {
	dbRev, behind, err := s.uc.ObserveRevision(ctx, serverID, agentRev)
	if err != nil || !behind {
		return
	}
	s.mu.Lock()
	// Equality, not >=: after a database restore the revision can come
	// back below one pushed before, and must be pushed again.
	if s.pushed[serverID] == dbRev {
		s.mu.Unlock()
		return
	}
	s.pushed[serverID] = dbRev
	s.mu.Unlock()
	pctx, cancel := context.WithTimeout(s.ctx, indexSyncPublishTimeout)
	defer cancel()
	if err := s.uc.Sync(pctx, serverID, nil); err != nil {
		s.log.Warn("sync shared variables: drift push", "error", err, "server_id", serverID)
		// Nothing was sent: the next report tries again.
		s.mu.Lock()
		if s.pushed[serverID] == dbRev {
			delete(s.pushed, serverID)
		}
		s.mu.Unlock()
	}
}

func (s *varsSync) reloaded(ctx context.Context, serverID string, payload []byte) {
	s.uc.Reloaded(ctx, serverID, payload)
}
