package sharedvars_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/sharedvars"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/db/repository/sharedvar"
	"winterflow/internal/infra/db/types"
)

var (
	org1, org2             = dbtest.ID(1), dbtest.ID(2)
	srvA, srvB, srvP, srvX = dbtest.ID(11), dbtest.ID(12), dbtest.ID(13), dbtest.ID(14)
	users                  = []string{"u1", "u2"}
)

type fakeDispatcher struct {
	mu   sync.Mutex
	sent []port.DispatchInput
}

func (f *fakeDispatcher) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, in)
	return "req-" + strconv.Itoa(len(f.sent)), nil
}

func (f *fakeDispatcher) of(typ command.Type) []port.DispatchInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []port.DispatchInput
	for _, in := range f.sent {
		if in.Type == typ {
			out = append(out, in)
		}
	}
	return out
}

// lastTo is the last command of typ sent to serverID.
func (f *fakeDispatcher) lastTo(t *testing.T, typ command.Type, serverID string) port.DispatchInput {
	t.Helper()
	all := f.of(typ)
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].AgentID == serverID {
			return all[i]
		}
	}
	t.Fatalf("no %s sent to %s", typ, serverID)
	return port.DispatchInput{}
}

type sent struct {
	user string
	n    model.Notification
}

type fakePublisher struct {
	mu  sync.Mutex
	got []sent
}

func (f *fakePublisher) Publish(userID string, n model.Notification) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, sent{userID, n})
}

func (f *fakePublisher) of(typ model.NotificationType) []model.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Notification
	for _, s := range f.got {
		if s.n.Type == typ {
			out = append(out, s.n)
		}
	}
	return out
}

type fakeRevisions struct {
	mu   sync.Mutex
	revs map[string]int64
}

func (f *fakeRevisions) SetVarsRevision(id string, rev int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revs[id] = rev
}

func (f *fakeRevisions) VarsRevision(id string) (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.revs[id]
	return r, ok
}

// queued is one reload put on the rollout queue.
type queued struct {
	user, org string
	scope     model.ReloadScope
}

type fakeQueue struct {
	mu  sync.Mutex
	got []queued
	err error // answered by every QueueReload
}

func (f *fakeQueue) QueueReload(_ context.Context, userID, orgID string, s model.ReloadScope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, queued{userID, orgID, s})
	return f.err
}

// last is the last reload queued.
func (f *fakeQueue) last(t *testing.T) queued {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) == 0 {
		t.Fatal("no reload queued")
	}
	return f.got[len(f.got)-1]
}

type env struct {
	uc    *sharedvars.UseCase
	d     *db.DB
	repo  *sharedvar.Repo
	disp  *fakeDispatcher
	pub   *fakePublisher
	revs  *fakeRevisions
	queue *fakeQueue
	caps  map[string]string // server -> stored public key
}

// fakeServers answers from env.caps; every server has users u1, u2.
type fakeServers struct{ e *env }

func (f fakeServers) GetCapability(_ context.Context, serverID, name string) (string, bool, error) {
	if name != "public_key" {
		return "", false, nil
	}
	v, ok := f.e.caps[serverID]
	return v, ok, nil
}

func (f fakeServers) GetServerUserIDs(context.Context, string) ([]string, error) { return users, nil }

func (e *env) seedServer(t *testing.T, orgID, serverID, name, key string) {
	t.Helper()
	dbtest.Exec(t, e.d, `INSERT INTO servers (server_id, organization_id, name, created_at) VALUES (?, ?, ?, ?)`,
		serverID, orgID, name, types.NewDateTime())
	if key != "" {
		e.setKey(t, serverID, key)
	}
}

func (e *env) setKey(t *testing.T, serverID, key string) {
	t.Helper()
	dbtest.Exec(t, e.d, `INSERT INTO server_capabilities (server_id, name, value, updated_at) VALUES (?, 'public_key', ?, ?)`,
		serverID, key, types.NewDateTime())
	e.caps[serverID] = key
}

// newEnv: org1 owns alpha (keyed), beta (keyed) and pending (no key yet);
// org2 owns foreign.
func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.Open(t)
	repo, err := sharedvar.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{d: d, repo: repo, disp: &fakeDispatcher{}, pub: &fakePublisher{},
		revs: &fakeRevisions{revs: map[string]int64{}}, queue: &fakeQueue{}, caps: map[string]string{}}
	dbtest.SeedOrg(t, d, org1)
	dbtest.SeedOrg(t, d, org2)
	e.seedServer(t, org1, srvA, "alpha", "KEY-A")
	e.seedServer(t, org1, srvB, "beta", "KEY-B")
	e.seedServer(t, org1, srvP, "pending", "")
	e.seedServer(t, org2, srvX, "foreign", "KEY-X")
	e.uc = sharedvars.New(&sharedvars.Deps{Repo: repo, Servers: fakeServers{e}, Dispatcher: e.disp,
		Notifications: e.pub, Revisions: e.revs, Reloads: e.queue, Log: dbtest.Log})
	return e
}

func ptr(s string) *string { return &s }

func invalid(t *testing.T, err error, mention string) {
	t.Helper()
	var ve *sharedvars.ValidationError
	if !errors.As(err, &ve) || !strings.Contains(ve.Msg, mention) {
		t.Fatalf("err = %v, want a validation error mentioning %q", err, mention)
	}
}

func agents(ins []port.DispatchInput) []string {
	out := []string{}
	for _, in := range ins {
		out = append(out, in.AgentID)
	}
	return out
}

func answer(t *testing.T, in port.DispatchInput, success bool, errMsg string, payload any) {
	t.Helper()
	res := port.CommandResult{Success: success, Error: errMsg}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		res.Payload = raw
	}
	in.OnResult(res)
}

func TestSaveOrgPlainPushesEveryOrgServer(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	v, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "SMTP_HOST", Value: ptr("mail"), Description: "relay"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Value != "mail" || v.Scope != "org" || v.UpdatedBy != "u1" || v.ID == "" || v.UpdatedAt.IsZero() {
		t.Fatalf("view = %+v", v)
	}
	syncs := e.disp.of(command.TypeVarsSync)
	if got := agents(syncs); !reflect.DeepEqual(got, []string{srvA, srvB, srvP}) {
		t.Fatalf("synced %v", got)
	}
	req := syncs[0].Payload.(command.SyncVarsRequest)
	if req.Revision != 1 || req.ServerName != "alpha" || req.OrgID != org1 ||
		!reflect.DeepEqual(req.Org, []command.SharedVar{{Name: "SMTP_HOST", Value: "mail"}}) || len(req.Server) != 0 {
		t.Fatalf("snapshot = %+v", req)
	}
	if syncs[0].UserID != "" {
		t.Fatal("syncs are dispatched for no user; results fan out to the org")
	}
	// Same value, new description: nothing for the agents.
	if _, err := e.uc.SaveOrg(ctx, "u2", org1, sharedvars.SaveInput{Name: "SMTP_HOST", Value: ptr("mail"), Description: "new"}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.disp.of(command.TypeVarsSync)); n != 3 {
		t.Fatalf("a description edit pushed %d syncs", n-3)
	}
	if info, _ := e.repo.ServerVarsInfo(ctx, srvX); info.Revision != 0 {
		t.Fatal("another org's server was bumped")
	}
}

func TestNamesAndKinds(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	_, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "WF_NOPE", Value: ptr("1")})
	invalid(t, err, "reserved")
	_, err = e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "A", Value: ptr("1"), Ciphertexts: map[string]string{srvA: "CT"}})
	invalid(t, err, "plain variable")
	_, err = e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "S", IsSecret: true, Value: ptr("plaintext!")})
	invalid(t, err, "ciphertexts")
	if _, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "S", IsSecret: true,
		Ciphertexts: map[string]string{srvA: "CT-A", srvB: "CT-B"}}); err != nil {
		t.Fatal(err)
	}
	_, err = e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "S", Value: ptr("now plain")})
	invalid(t, err, "delete it and create it again")
}

func TestSecretCoverageRules(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	save := func(cts map[string]string) (sharedvars.VariableView, error) {
		return e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "SMTP_PASS", IsSecret: true, Ciphertexts: cts})
	}
	_, err := save(map[string]string{srvA: "CT-A"})
	invalid(t, err, srvB) // the pending server has no key, so only beta is uncovered
	if _, err := save(map[string]string{srvA: "CT-A", srvB: "CT-B", srvX: "CT-X"}); !errors.Is(err, sharedvars.ErrForbidden) {
		t.Fatalf("foreign ciphertext err = %v", err)
	}
	_, err = save(map[string]string{srvA: "CT-A", srvB: "CT-B", srvP: "CT-P"})
	invalid(t, err, "no public key")

	v, err := save(map[string]string{srvA: "CT-A", srvB: "CT-B"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Value != command.EncryptedPlaceholder || len(v.MissingOn) != 0 {
		t.Fatalf("view = %+v", v)
	}
	// A partial map that would overwrite alpha's copy is not a gap fill.
	_, err = save(map[string]string{srvA: "ROT-A"})
	invalid(t, err, srvB)

	// The pending server reports a key: the secret is now missing there.
	e.setKey(t, srvP, "KEY-P")
	list, err := e.uc.ListOrg(ctx, org1)
	if err != nil || len(list.Variables) != 1 || !reflect.DeepEqual(list.Variables[0].MissingOn, []string{srvP}) {
		t.Fatalf("list = %+v, %v", list, err)
	}
	// A "gap fill" that also names an already-covered server is a new value
	// that misses beta: refused, and alpha's stored copy is not overwritten
	// (the repository upserts ciphertexts, so this must stop in the usecase).
	before := len(e.disp.of(command.TypeVarsSync))
	_, err = save(map[string]string{srvA: "OVERWRITE-A", srvP: "CT-P"})
	invalid(t, err, srvB)
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvA); cts[v.ID] != "CT-A" {
		t.Fatalf("refused gap fill overwrote alpha: %v", cts)
	}
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvP); len(cts) != 0 {
		t.Fatalf("refused gap fill stored pending's copy: %v", cts)
	}
	if n := len(e.disp.of(command.TypeVarsSync)) - before; n != 0 {
		t.Fatalf("refused gap fill pushed %d syncs", n)
	}
	if _, err := save(map[string]string{srvP: "CT-P"}); err != nil { // gap fill
		t.Fatal(err)
	}
	if got := agents(e.disp.of(command.TypeVarsSync)[before:]); !reflect.DeepEqual(got, []string{srvP}) {
		t.Fatalf("gap fill pushed to %v, want only the filled server", got)
	}
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvA); cts[v.ID] != "CT-A" {
		t.Fatalf("gap fill touched alpha: %v", cts)
	}
	// No ciphertexts at all: a description edit keeps every copy.
	if _, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "SMTP_PASS", IsSecret: true, Description: "monthly"}); err != nil {
		t.Fatal(err)
	}
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvP); cts[v.ID] != "CT-P" {
		t.Fatalf("description edit dropped a copy: %v", cts)
	}
	// A full new value replaces every copy and pushes everywhere.
	before = len(e.disp.of(command.TypeVarsSync))
	if _, err := save(map[string]string{srvA: "R-A", srvB: "R-B", srvP: "R-P"}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.disp.of(command.TypeVarsSync)) - before; n != 3 {
		t.Fatalf("rotation pushed %d syncs", n)
	}
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvA); cts[v.ID] != "R-A" {
		t.Fatalf("rotation = %v", cts)
	}
}

func TestEncryptionKeys(t *testing.T) {
	e := newEnv(t)
	keys, err := e.uc.EncryptionKeys(dbtest.Ctx(t), org1)
	if err != nil {
		t.Fatal(err)
	}
	want := sharedvars.EncryptionKeys{
		Servers:        []sharedvars.EncryptionKey{{ServerID: srvA, Name: "alpha", PublicKey: "KEY-A"}, {ServerID: srvB, Name: "beta", PublicKey: "KEY-B"}},
		PendingServers: []sharedvars.PendingServer{{ServerID: srvP, Name: "pending"}},
	}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %+v", keys)
	}
}

func TestServerScopeAndServerListing(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	mustSave := func(v sharedvars.VariableView, err error) sharedvars.VariableView {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	mustSave(e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "SMTP_HOST", Value: ptr("org-mail")}))
	mustSave(e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "TOKEN", IsSecret: true, Ciphertexts: map[string]string{srvA: "T-A", srvB: "T-B"}}))
	before := len(e.disp.of(command.TypeVarsSync))
	mustSave(e.uc.SaveServer(ctx, "u1", srvA, sharedvars.SaveInput{Name: "SMTP_HOST", Value: ptr("local")}))
	if got := agents(e.disp.of(command.TypeVarsSync)[before:]); !reflect.DeepEqual(got, []string{srvA}) {
		t.Fatalf("server save pushed to %v", got)
	}
	_, err := e.uc.SaveServer(ctx, "u1", srvA, sharedvars.SaveInput{Name: "KEY", IsSecret: true, Ciphertexts: map[string]string{srvB: "K-B"}})
	invalid(t, err, "its own server")
	mustSave(e.uc.SaveServer(ctx, "u1", srvA, sharedvars.SaveInput{Name: "KEY", IsSecret: true, Ciphertexts: map[string]string{srvA: "K-A"}}))

	ls, err := e.uc.ListServer(ctx, srvA)
	if err != nil {
		t.Fatal(err)
	}
	if len(ls.Variables) != 2 || ls.Variables[0].Name != "KEY" || ls.Variables[1].Value != "local" {
		t.Fatalf("own = %+v", ls.Variables)
	}
	if len(ls.Inherited) != 2 || ls.Inherited[0].Name != "SMTP_HOST" || !ls.Inherited[0].Overridden ||
		ls.Inherited[1].Name != "TOKEN" || ls.Inherited[1].Overridden || ls.Inherited[1].Value != command.EncryptedPlaceholder {
		t.Fatalf("inherited = %+v", ls.Inherited)
	}
	if len(ls.MissingOrgSecrets) != 0 {
		t.Fatalf("missing = %v", ls.MissingOrgSecrets)
	}
	names := map[string]string{}
	for _, b := range ls.BuiltIns {
		names[b.Name] = b.Value
	}
	if names["WF_SERVER_NAME"] != "alpha" || names["WF_ORG_ID"] != org1 || len(ls.BuiltIns) != 8 {
		t.Fatalf("builtins = %+v", ls.BuiltIns)
	}
	if ls.Sync.ServerID != srvA || ls.Sync.State != sharedvars.SyncPending || !ls.Sync.HasKey || ls.Sync.AgentRevision != nil {
		t.Fatalf("sync = %+v", ls.Sync)
	}
	e.revs.SetVarsRevision(srvA, ls.Sync.Revision)
	if ls, _ := e.uc.ListServer(ctx, srvA); ls.Sync.State != sharedvars.SyncSynced {
		t.Fatalf("caught-up agent = %+v", ls.Sync)
	}
	e.setKey(t, srvP, "KEY-P")
	if ls, _ := e.uc.ListServer(ctx, srvP); !reflect.DeepEqual(ls.MissingOrgSecrets, []string{"TOKEN"}) {
		t.Fatalf("pending server missing = %v", ls.MissingOrgSecrets)
	}
	// The org list never shows server-scope variables.
	if org, _ := e.uc.ListOrg(ctx, org1); len(org.Variables) != 2 || len(org.Servers) != 3 {
		t.Fatalf("org list = %+v", org)
	}
}

func TestDeleteQueuesTheReload(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	v, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "SMTP_HOST", Value: ptr("mail")})
	if err != nil {
		t.Fatal(err)
	}
	invalid(t, e.uc.DeleteOrg(ctx, "u2", org2, v.ID, false), "not found")
	invalid(t, e.uc.DeleteServer(ctx, "u1", srvA, v.ID, false), "not found")
	before := len(e.disp.of(command.TypeVarsSync))
	if err := e.uc.DeleteOrg(ctx, "u1", org1, v.ID, true); err != nil {
		t.Fatal(err)
	}
	if n := len(e.disp.of(command.TypeVarsSync)) - before; n != 3 {
		t.Fatalf("delete pushed %d syncs", n)
	}
	syncA := e.disp.lastTo(t, command.TypeVarsSync, srvA)
	if len(syncA.Payload.(command.SyncVarsRequest).Org) != 0 {
		t.Fatal("the deleted variable is still in the snapshot")
	}
	// The reload goes on the org's rollout, at the revision the delete
	// bumped every server to; nothing is sent to the agents directly.
	q := e.queue.last(t)
	want := model.ReloadScope{Names: []string{"SMTP_HOST"}, ServerIDs: []string{srvA, srvB, srvP},
		Revisions: map[string]int64{srvA: 2, srvB: 2, srvP: 2}}
	if q.user != "u1" || q.org != org1 || !reflect.DeepEqual(q.scope, want) {
		t.Fatalf("queued = %+v, want %+v", q, want)
	}
	answer(t, syncA, true, "", command.SyncVarsResponse{Revision: 2, Undecryptable: []command.VarRef{}})
	if n := len(e.disp.of(command.TypeVarsReload)); n != 0 {
		t.Fatalf("%d vars.reload sent directly", n)
	}
}

// A reload the queue refuses (the org's rollout is being stopped) fails the
// reload route with that error; a save or delete with reload: true has
// committed already and only logs it.
func TestAQueueErrorFailsTheReloadRouteButNotASave(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	e.queue.err = model.ErrRolloutStopping
	if _, err := e.uc.Reload(ctx, "u1", org1, sharedvars.ReloadInput{ServerID: srvA}); !errors.Is(err, model.ErrRolloutStopping) {
		t.Fatalf("reload err = %v", err)
	}
	v, err := e.uc.SaveServer(ctx, "u1", srvB, sharedvars.SaveInput{Name: "LOCAL", Value: ptr("x"), Reload: true})
	if err != nil {
		t.Fatalf("save err = %v", err)
	}
	if err := e.uc.DeleteServer(ctx, "u1", srvB, v.ID, true); err != nil {
		t.Fatalf("delete err = %v", err)
	}
}

func TestSaveWithReloadQueuesOnlyWhenSomethingChanged(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	if _, err := e.uc.SaveServer(ctx, "u1", srvB, sharedvars.SaveInput{Name: "LOCAL", Value: ptr("x"), Reload: true}); err != nil {
		t.Fatal(err)
	}
	q := e.queue.last(t)
	if !reflect.DeepEqual(q.scope, model.ReloadScope{Names: []string{"LOCAL"}, ServerIDs: []string{srvB}, Revisions: map[string]int64{srvB: 1}}) {
		t.Fatalf("queued = %+v", q)
	}
	// A description-only edit changes no server: nothing to reload.
	if _, err := e.uc.SaveServer(ctx, "u1", srvB, sharedvars.SaveInput{Name: "LOCAL", Value: ptr("x"), Description: "d", Reload: true}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.queue.got); n != 1 {
		t.Fatalf("queued %d reloads", n)
	}
}

func TestSyncResults(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	v, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "TOKEN", IsSecret: true, Ciphertexts: map[string]string{srvA: "T-A", srvB: "T-B"}})
	if err != nil {
		t.Fatal(err)
	}
	syncA := e.disp.lastTo(t, command.TypeVarsSync, srvA)
	if got := syncA.Payload.(command.SyncVarsRequest).Org; !reflect.DeepEqual(got, []command.SharedVar{{Name: "TOKEN", IsSecret: true, Ciphertext: "T-A"}}) {
		t.Fatalf("alpha's snapshot = %+v (only alpha's ciphertext may travel to alpha)", got)
	}
	if got := e.disp.lastTo(t, command.TypeVarsSync, srvP).Payload.(command.SyncVarsRequest).Org; got[0].Ciphertext != "" || !got[0].IsSecret {
		t.Fatalf("pending server's snapshot = %+v (name only)", got)
	}
	answer(t, syncA, true, "", command.SyncVarsResponse{Revision: 1, Undecryptable: []command.VarRef{{Scope: "org", Name: "TOKEN"}}})
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvA); len(cts) != 0 {
		t.Fatalf("undecryptable copy kept: %v", cts)
	}
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvB); cts[v.ID] != "T-B" {
		t.Fatalf("beta's copy dropped too: %v", cts)
	}
	synced := e.pub.of(model.NotificationVarsSynced)
	if len(synced) != len(users) {
		t.Fatalf("vars.synced sent %d times", len(synced))
	}
	p := synced[0].Payload.(model.VarsSyncedPayload)
	if p.ServerID != srvA || p.Revision != 1 || !reflect.DeepEqual(p.Undecryptable, []string{"TOKEN"}) {
		t.Fatalf("payload = %+v", p)
	}
	if rev, ok := e.revs.VarsRevision(srvA); !ok || rev != 1 {
		t.Fatalf("cached revision = %d %v", rev, ok)
	}
}

func TestSyncNotConnectedStaysPending(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	if _, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "A", Value: ptr("1")}); err != nil {
		t.Fatal(err)
	}
	syncB := e.disp.lastTo(t, command.TypeVarsSync, srvB)
	answer(t, syncB, false, "agent not connected", nil)
	if n := len(e.pub.got); n != 0 {
		t.Fatalf("an offline agent produced %d notifications", n)
	}
	answer(t, syncB, false, "unsupported command type: vars.sync", nil)
	failed := e.pub.of(model.NotificationVarsSyncFailed)
	if len(failed) != len(users) || failed[0].Payload.(model.VarsSyncFailedPayload).Error != "unsupported command type: vars.sync" {
		t.Fatalf("failed = %+v", failed)
	}
}

func TestKeyChangePurgesThatServersCopies(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	v, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "TOKEN", IsSecret: true, Ciphertexts: map[string]string{srvA: "T-A", srvB: "T-B"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		server string
		caps   map[string]string
	}{
		{srvA, map[string]string{"public_key": "KEY-A", "os": "linux"}}, // unchanged
		{srvA, map[string]string{"os": "linux"}},                        // no key reported
		{srvP, map[string]string{"public_key": "FIRST-KEY"}},            // first report
	} {
		if e.uc.KeyObserved(ctx, c.server, c.caps) {
			t.Fatalf("reported a purge for %s %v", c.server, c.caps)
		}
	}
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvA); cts[v.ID] != "T-A" {
		t.Fatal("purged without a key change")
	}
	before, _ := e.repo.ServerVarsInfo(ctx, srvA)
	if !e.uc.KeyObserved(ctx, srvA, map[string]string{"public_key": "KEY-A2"}) {
		t.Fatal("a key change did not report its purge")
	}
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvA); len(cts) != 0 {
		t.Fatalf("copies for the old key kept: %v", cts)
	}
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvB); cts[v.ID] != "T-B" {
		t.Fatal("another server's copy purged")
	}
	if after, _ := e.repo.ServerVarsInfo(ctx, srvA); after.Revision != before.Revision+1 {
		t.Fatalf("revision %d -> %d", before.Revision, after.Revision)
	}
}

func TestObserveRevision(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	if _, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "A", Value: ptr("1")}); err != nil {
		t.Fatal(err)
	}
	if rev, behind, err := e.uc.ObserveRevision(ctx, srvA, 0); err != nil || rev != 1 || !behind {
		t.Fatalf("behind agent = %d %v %v", rev, behind, err)
	}
	if _, behind, _ := e.uc.ObserveRevision(ctx, srvA, 1); behind {
		t.Fatal("a caught-up agent counted as behind")
	}
	if rev, ok := e.revs.VarsRevision(srvA); !ok || rev != 1 {
		t.Fatalf("cache = %d %v", rev, ok)
	}
}

func TestResyncAndReloadTargets(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	local, err := e.uc.SaveServer(ctx, "u1", srvB, sharedvars.SaveInput{Name: "LOCAL", Value: ptr("x")})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := e.uc.Resync(ctx, org1, sharedvars.ResyncInput{}); err != nil || !reflect.DeepEqual(got, []string{srvA, srvB, srvP}) {
		t.Fatalf("resync all = %v %v", got, err)
	}
	if _, err := e.uc.Resync(ctx, org1, sharedvars.ResyncInput{ServerID: srvX}); !errors.Is(err, sharedvars.ErrForbidden) {
		t.Fatalf("foreign resync err = %v", err)
	}
	if got, _ := e.uc.Resync(ctx, org1, sharedvars.ResyncInput{VariableID: local.ID}); !reflect.DeepEqual(got, []string{srvB}) {
		t.Fatalf("resync by variable = %v", got)
	}
	_, err = e.uc.Reload(ctx, "u1", org1, sharedvars.ReloadInput{})
	invalid(t, err, "required")
	_, err = e.uc.Reload(ctx, "u1", org1, sharedvars.ReloadInput{ServerID: srvA, AppIDs: []string{"../x"}})
	invalid(t, err, "invalid app id")
	if _, err := e.uc.Reload(ctx, "u1", org1, sharedvars.ReloadInput{ServerID: srvX}); !errors.Is(err, sharedvars.ErrForbidden) {
		t.Fatalf("foreign reload err = %v", err)
	}
	cases := []struct {
		in      sharedvars.ReloadInput
		servers []string
		scope   model.ReloadScope
	}{
		{sharedvars.ReloadInput{ServerID: srvA, AppIDs: []string{"a1"}}, []string{srvA},
			model.ReloadScope{AppIDs: []string{"a1"}, ServerIDs: []string{srvA}, Revisions: map[string]int64{srvA: 0}}},
		{sharedvars.ReloadInput{ServerID: srvA, Names: []string{"SMTP_PASS"}}, []string{srvA},
			model.ReloadScope{Names: []string{"SMTP_PASS"}, ServerIDs: []string{srvA}, Revisions: map[string]int64{srvA: 0}}},
		{sharedvars.ReloadInput{ServerID: srvA}, []string{srvA},
			model.ReloadScope{AllStale: []string{srvA}, Revisions: map[string]int64{srvA: 0}}},
		{sharedvars.ReloadInput{VariableID: local.ID}, []string{srvB},
			model.ReloadScope{Names: []string{"LOCAL"}, ServerIDs: []string{srvB}, Revisions: map[string]int64{srvB: 1}}},
	}
	for _, tc := range cases {
		got, err := e.uc.Reload(ctx, "u1", org1, tc.in)
		if q := e.queue.last(t); err != nil || !reflect.DeepEqual(got, tc.servers) || !reflect.DeepEqual(q.scope, tc.scope) || q.org != org1 {
			t.Errorf("%+v: servers %v, queued %+v, %v", tc.in, got, q, err)
		}
	}
	if n := len(e.disp.of(command.TypeVarsReload)); n != 0 {
		t.Fatalf("%d vars.reload sent directly", n)
	}
	// Without a queue a reload is refused, not silently dropped.
	bare := sharedvars.New(&sharedvars.Deps{Repo: e.repo, Servers: fakeServers{e}, Dispatcher: e.disp, Log: dbtest.Log})
	if _, err := bare.Reload(ctx, "u1", org1, sharedvars.ReloadInput{ServerID: srvA}); err == nil {
		t.Fatal("a reload without a queue succeeded")
	}
}

func TestReloadedFansOutWithServerID(t *testing.T) {
	e := newEnv(t)
	e.uc.Reloaded(dbtest.Ctx(t), srvA, []byte(`{"app_id":"a1","ok":true,"names":["SMTP_PASS"]}`))
	got := e.pub.of(model.NotificationVarsReloaded)
	if len(got) != len(users) {
		t.Fatalf("sent %d", len(got))
	}
	p := got[0].Payload.(command.VarsReloadedPayload)
	if p.ServerID != srvA || p.AppID != "a1" || !p.OK || p.Names[0] != "SMTP_PASS" {
		t.Fatalf("payload = %+v", p)
	}
	e.uc.Reloaded(dbtest.Ctx(t), srvA, []byte(`not json`)) // logged, not sent
	if n := len(e.pub.of(model.NotificationVarsReloaded)); n != len(users) {
		t.Fatalf("malformed outcome fanned out (%d)", n)
	}
}

func TestPlainSaveRequiresValue(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	_, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "SMTP_HOST", Description: "relay"})
	invalid(t, err, "value")
	if _, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "SMTP_HOST", Value: ptr("mail")}); err != nil {
		t.Fatal(err)
	}
	before := len(e.disp.of(command.TypeVarsSync))
	_, err = e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "SMTP_HOST", Description: "edited"})
	invalid(t, err, "value")
	list, err := e.uc.ListOrg(ctx, org1)
	if err != nil || len(list.Variables) != 1 || list.Variables[0].Value != "mail" || list.Variables[0].Description != "" {
		t.Fatalf("an omitted value changed the stored variable: %+v, %v", list, err)
	}
	if n := len(e.disp.of(command.TypeVarsSync)) - before; n != 0 {
		t.Fatalf("a refused save pushed %d syncs", n)
	}
	// An explicit empty value is a real value.
	if v, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "SMTP_HOST", Value: ptr("")}); err != nil || v.Value != "" {
		t.Fatalf("explicit empty value = %+v, %v", v, err)
	}
}

// TestObserveRevisionRaisesPastAnAheadAgent: an agent holding a revision the
// database never issued (restored DB) would ignore every push at or below
// it, so the revision is raised past the agent's and the agent is behind.
func TestObserveRevisionRaisesPastAnAheadAgent(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	rev, behind, err := e.uc.ObserveRevision(ctx, srvA, 5)
	if err != nil || rev != 6 || !behind {
		t.Fatalf("ahead agent = %d %v %v, want 6 behind", rev, behind, err)
	}
	if info, _ := e.repo.ServerVarsInfo(ctx, srvA); info.Revision != 6 {
		t.Fatalf("stored revision = %d, want 6", info.Revision)
	}
	if rev, behind, _ := e.uc.ObserveRevision(ctx, srvA, 6); rev != 6 || behind {
		t.Fatalf("caught-up agent = %d %v", rev, behind)
	}
	if info, _ := e.repo.ServerVarsInfo(ctx, srvB); info.Revision != 0 {
		t.Fatalf("another server raised to %d", info.Revision)
	}
}

// TestLateSyncResultKeepsNewerCiphertexts: a vars.sync result for an older
// revision must not drop a ciphertext replaced since; the result for the
// current revision does. The cached agent revision never moves back.
func TestLateSyncResultKeepsNewerCiphertexts(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	v, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "TOKEN", IsSecret: true, Ciphertexts: map[string]string{srvA: "T-A", srvB: "T-B"}})
	if err != nil {
		t.Fatal(err)
	}
	old := e.disp.lastTo(t, command.TypeVarsSync, srvA)
	if _, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "TOKEN", IsSecret: true, Ciphertexts: map[string]string{srvA: "T-A2", srvB: "T-B2"}}); err != nil {
		t.Fatal(err)
	}
	cur := e.disp.lastTo(t, command.TypeVarsSync, srvA)
	curRev := cur.Payload.(command.SyncVarsRequest).Revision
	if curRev != 2 {
		t.Fatalf("current revision = %d", curRev)
	}
	undecryptable := []command.VarRef{{Scope: "org", Name: "TOKEN"}}

	answer(t, cur, true, "", command.SyncVarsResponse{Revision: 2})
	answer(t, old, true, "", command.SyncVarsResponse{Revision: 1, Undecryptable: undecryptable})
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvA); cts[v.ID] != "T-A2" {
		t.Fatalf("a stale result dropped the newer ciphertext: %v", cts)
	}
	if got := e.pub.of(model.NotificationVarsSynced); len(got) == 0 || len(got[len(got)-1].Payload.(model.VarsSyncedPayload).Undecryptable) != 0 {
		t.Fatalf("a stale result announced undecryptable names: %+v", got)
	}
	if rev, _ := e.revs.VarsRevision(srvA); rev != 2 {
		t.Fatalf("cached revision moved back to %d", rev)
	}

	answer(t, cur, true, "", command.SyncVarsResponse{Revision: 2, Undecryptable: undecryptable})
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvA); len(cts) != 0 {
		t.Fatalf("the current result kept an undecryptable copy: %v", cts)
	}
}
