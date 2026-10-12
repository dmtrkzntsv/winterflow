package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/sharedvars"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/db/repository/sharedvar"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/logger"
)

type varsDispatcher struct {
	mu   sync.Mutex
	fail bool // the publish itself fails
	sent []port.DispatchInput
}

func (d *varsDispatcher) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail {
		return "", errors.New("bus down")
	}
	d.sent = append(d.sent, in)
	return "req-" + strconv.Itoa(len(d.sent)), nil
}

func (d *varsDispatcher) setFail(fail bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fail = fail
}

func (d *varsDispatcher) syncs() []port.DispatchInput {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []port.DispatchInput
	for _, in := range d.sent {
		if in.Type == command.TypeVarsSync {
			out = append(out, in)
		}
	}
	return out
}

// keyDirectory answers the stored public_key from a map.
type keyDirectory map[string]string

func (k keyDirectory) GetCapability(_ context.Context, serverID, name string) (string, bool, error) {
	v, ok := k[serverID]
	return v, ok && name == "public_key", nil
}

func (keyDirectory) GetServerUserIDs(context.Context, string) ([]string, error) { return nil, nil }

var (
	varsOrg = dbtest.ID(1)
	varsSrv = dbtest.ID(11)
)

type varsEnv struct {
	s     *varsSync
	repo  *sharedvar.Repo
	disp  *varsDispatcher
	bumps int
}

func newTestVarsSync(t *testing.T) *varsEnv {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, varsOrg, varsSrv)
	repo, err := sharedvar.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	disp := &varsDispatcher{}
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	s := newVarsSync(context.Background(), disp, sharedvars.Deps{Repo: repo, Servers: keyDirectory{varsSrv: "K1"}, Log: log}, log)
	s.retryDelay = time.Hour // retries are driven by the test
	return &varsEnv{s: s, repo: repo, disp: disp}
}

// bump records a change on varsSrv (a new plain org variable).
func (e *varsEnv) bump(t *testing.T) {
	t.Helper()
	e.bumps++
	if err := e.repo.Save(dbtest.Ctx(t), model.SharedVariableSave{Create: true, Bump: []string{varsSrv},
		Variable: model.SharedVariable{ID: dbtest.ID(100 + e.bumps), OrgID: varsOrg, Name: "V" + strconv.Itoa(e.bumps), Value: "x"}}); err != nil {
		t.Fatal(err)
	}
}

func revOf(in port.DispatchInput) int64 { return in.Payload.(command.SyncVarsRequest).Revision }

func answerSync(in port.DispatchInput, rev int64) {
	raw, _ := json.Marshal(command.SyncVarsResponse{Revision: rev, Undecryptable: []command.VarRef{}})
	in.OnResult(port.CommandResult{Success: true, Payload: raw})
}

func TestDriftPushesOncePerRevision(t *testing.T) {
	e := newTestVarsSync(t)
	s, disp := e.s, e.disp
	ctx := dbtest.Ctx(t)
	e.bump(t) // revision 1
	s.revision(ctx, varsSrv, 0)
	s.revision(ctx, varsSrv, 0) // same drift, next report: no second push
	if n := len(disp.syncs()); n != 1 {
		t.Fatalf("pushes = %d, want 1", n)
	}
	if req := disp.syncs()[0].Payload.(command.SyncVarsRequest); req.Revision != 1 || len(req.Org) != 1 {
		t.Fatalf("pushed %+v", req)
	}
	e.bump(t) // revision 2: a new drift
	s.revision(ctx, varsSrv, 1)
	s.revision(ctx, varsSrv, 2) // caught up
	if n := len(disp.syncs()); n != 2 {
		t.Fatalf("pushes = %d, want 2", n)
	}
	s.revision(ctx, dbtest.ID(99), 0) // an unknown server: nothing, no panic
	if n := len(disp.syncs()); n != 2 {
		t.Fatalf("unknown server pushed (%d)", n)
	}
}

// TestDriftPushFailureIsRetriedOnNextReport: a push that never left (the
// publish failed) does not count as pushed.
func TestDriftPushFailureIsRetriedOnNextReport(t *testing.T) {
	e := newTestVarsSync(t)
	ctx := dbtest.Ctx(t)
	e.bump(t)
	e.disp.setFail(true)
	e.s.revision(ctx, varsSrv, 0)
	e.disp.setFail(false)
	e.s.revision(ctx, varsSrv, 0)
	if got := e.disp.syncs(); len(got) != 1 || revOf(got[0]) != 1 {
		t.Fatalf("pushes = %+v, want one at revision 1", got)
	}
}

// TestDriftRaisesPastAnAheadAgent: an agent reporting a revision above
// servers.vars_revision (restored database) ignores every snapshot at or
// below its own, so the revision is raised past it and pushed, once.
func TestDriftRaisesPastAnAheadAgent(t *testing.T) {
	e := newTestVarsSync(t)
	ctx := dbtest.Ctx(t)
	e.s.revision(ctx, varsSrv, 5)
	e.s.revision(ctx, varsSrv, 5)
	got := e.disp.syncs()
	if len(got) != 1 || revOf(got[0]) != 6 {
		t.Fatalf("pushes = %d (rev %v), want one at revision 6", len(got), got)
	}
	if info, _ := e.repo.ServerVarsInfo(ctx, varsSrv); info.Revision != 6 {
		t.Fatalf("stored revision = %d", info.Revision)
	}
	e.s.revision(ctx, varsSrv, 6) // the agent took it
	if n := len(e.disp.syncs()); n != 1 {
		t.Fatalf("pushed a caught-up agent (%d)", n)
	}
}

// TestDriftRepushesARevisionLoweredByRestore: a revision pushed before a
// database restore can come back below the last push and still needs one.
func TestDriftRepushesARevisionLoweredByRestore(t *testing.T) {
	e := newTestVarsSync(t)
	ctx := dbtest.Ctx(t)
	e.s.revision(ctx, varsSrv, 9) // raised to 10, pushed
	e.s.mu.Lock()
	e.s.pushed[varsSrv] = 12 // as if 12 was pushed before the restore
	e.s.mu.Unlock()
	e.s.revision(ctx, varsSrv, 10) // caught up with 10
	e.bump(t)                      // 11 < 12
	e.s.revision(ctx, varsSrv, 10)
	got := e.disp.syncs()
	if len(got) != 2 || revOf(got[1]) != 11 {
		t.Fatalf("pushes = %d, want a second one at revision 11", len(got))
	}
}

func TestVarsSyncOnConnectRetriesWhileNotConnected(t *testing.T) {
	e := newTestVarsSync(t)
	s, disp := e.s, e.disp
	s.online(varsSrv)
	first := disp.syncs()
	if len(first) != 1 || first[0].UserID != "" {
		t.Fatalf("connect pushes = %+v", first)
	}
	first[0].OnResult(port.CommandResult{Error: bus.ErrAgentNotConnected})
	s.heartbeat(varsSrv) // the stream is up now
	if n := len(disp.syncs()); n != 2 {
		t.Fatalf("pushes after heartbeat = %d, want 2", n)
	}
	answerSync(disp.syncs()[1], 0)
	s.heartbeat(varsSrv) // settled: no more pushes
	if n := len(disp.syncs()); n != 2 {
		t.Fatalf("pushes after success = %d", n)
	}
}

// TestUnrelatedResultKeepsConnectSyncPending: a drift push's result does
// not settle the pending connect sync; only the connect sync's own does.
func TestUnrelatedResultKeepsConnectSyncPending(t *testing.T) {
	e := newTestVarsSync(t)
	ctx := dbtest.Ctx(t)
	e.bump(t)
	e.s.online(varsSrv)           // connect sync (attempt 1), unanswered
	e.s.revision(ctx, varsSrv, 0) // drift push
	connect, drift := e.disp.syncs()[0], e.disp.syncs()[1]
	answerSync(drift, 1) // must not clear the pending connect sync
	connect.OnResult(port.CommandResult{Error: bus.ErrAgentNotConnected})
	e.s.heartbeat(varsSrv)
	if n := len(e.disp.syncs()); n != 3 {
		t.Fatalf("pushes = %d, want 3: an unrelated result cleared the pending connect sync", n)
	}
	// A late repeat of attempt 1's answer settles nothing either.
	answerSync(connect, 1)
	e.s.domainSync.mu.Lock()
	_, pending := e.s.pending[varsSrv]
	e.s.domainSync.mu.Unlock()
	if !pending {
		t.Fatal("a superseded attempt's result settled the sync")
	}
	answerSync(e.disp.syncs()[2], 1)
	e.s.domainSync.mu.Lock()
	_, pending = e.s.pending[varsSrv]
	e.s.domainSync.mu.Unlock()
	if pending {
		t.Fatal("the current attempt's result did not settle the sync")
	}
}

// TestKeyChangePushesThePurgedSnapshot: a changed key purges and bumps,
// then pushes even while a connect sync is in flight; an unchanged key
// does nothing.
func TestKeyChangePushesThePurgedSnapshot(t *testing.T) {
	e := newTestVarsSync(t)
	ctx := dbtest.Ctx(t)
	e.s.online(varsSrv) // in flight, unanswered
	e.s.capabilities(ctx, varsSrv, map[string]string{"public_key": "K1"})
	if n := len(e.disp.syncs()); n != 1 {
		t.Fatalf("an unchanged key pushed (%d)", n)
	}
	e.s.capabilities(ctx, varsSrv, map[string]string{"public_key": "K2"})
	got := e.disp.syncs()
	if len(got) != 2 || revOf(got[1]) != 1 {
		t.Fatalf("pushes = %d, want a second one at the bumped revision 1", len(got))
	}
	e.s.online(varsSrv) // the sink's online after the capabilities: in flight
	if n := len(e.disp.syncs()); n != 2 {
		t.Fatalf("stacked a push on the key-change push (%d)", n)
	}
}
