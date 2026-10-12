package sharedvar

import (
	"errors"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/db/types"
)

var (
	org1, org2       = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2, srv3 = dbtest.ID(11), dbtest.ID(12), dbtest.ID(13)
)

// seedServer inserts a server of orgID (the org must exist).
func seedServer(t *testing.T, d *db.DB, orgID, serverID, name string) {
	t.Helper()
	dbtest.Exec(t, d, `INSERT INTO servers (server_id, organization_id, name, created_at) VALUES (?, ?, ?, ?)`,
		serverID, orgID, name, types.NewDateTime())
}

func setKey(t *testing.T, d *db.DB, serverID, key string) {
	t.Helper()
	dbtest.Exec(t, d, `INSERT INTO server_capabilities (server_id, name, value, updated_at) VALUES (?, 'public_key', ?, ?)`,
		serverID, key, types.NewDateTime())
}

// newRepo: org1 owns srv1 (keyed) and srv2 (pending); org2 owns srv3.
func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedOrg(t, d, org1)
	dbtest.SeedOrg(t, d, org2)
	// Same-second created_at ties break on server_id, so srv1 lists first.
	seedServer(t, d, org1, srv1, "alpha")
	seedServer(t, d, org1, srv2, "beta")
	seedServer(t, d, org2, srv3, "gamma")
	setKey(t, d, srv1, "KEY-1")
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func revision(t *testing.T, d *db.DB, serverID string) string {
	t.Helper()
	return dbtest.QueryString(t, d, `SELECT CAST(vars_revision AS TEXT) FROM servers WHERE server_id = ?`, serverID)
}

func ptr(s string) *string { return &s }

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestSaveListGetAndBump(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	plain := model.SharedVariable{ID: dbtest.ID(101), OrgID: org1, Name: "SMTP_HOST", Value: "mail.example", Description: "relay", UpdatedBy: "u1"}
	if err := r.Save(ctx, model.SharedVariableSave{Variable: plain, Create: true, Bump: []string{srv1, srv2}}); err != nil {
		t.Fatal(err)
	}
	secret := model.SharedVariable{ID: dbtest.ID(102), OrgID: org1, Name: "SMTP_PASS", IsSecret: true}
	if err := r.Save(ctx, model.SharedVariableSave{Variable: secret, Create: true,
		Ciphertexts: map[string]string{srv1: "CT-1"}, Bump: []string{srv1}}); err != nil {
		t.Fatal(err)
	}
	local := model.SharedVariable{ID: dbtest.ID(103), OrgID: org1, ServerID: ptr(srv1), Name: "SMTP_HOST", Value: "local"}
	if err := r.Save(ctx, model.SharedVariableSave{Variable: local, Create: true, Bump: []string{srv1}}); err != nil {
		t.Fatal(err)
	}

	got, err := r.ListOrg(ctx, org1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "SMTP_HOST" || got[0].Value != "mail.example" || got[0].Description != "relay" ||
		got[0].UpdatedBy != "u1" || got[0].ServerID != nil || got[1].Name != "SMTP_PASS" || got[1].Value != "" ||
		len(got[1].CipherServers) != 1 || got[1].CipherServers[0] != srv1 {
		t.Fatalf("org list = %+v", got)
	}
	if got[0].UpdatedAt.IsZero() || got[0].UpdatedAt.Location() != time.UTC {
		t.Fatalf("updated_at = %v", got[0].UpdatedAt)
	}
	srvVars, err := r.ListServer(ctx, srv1)
	if err != nil || len(srvVars) != 1 || srvVars[0].Value != "local" || srvVars[0].Scope() != model.SharedScopeServer {
		t.Fatalf("server list = %+v, %v", srvVars, err)
	}
	if empty, err := r.ListServer(ctx, srv2); err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty server list = %#v, %v", empty, err)
	}
	g, err := r.Get(ctx, secret.ID)
	if err != nil || !g.IsSecret || len(g.CipherServers) != 1 {
		t.Fatalf("get = %+v, %v", g, err)
	}
	if _, err := r.Get(ctx, dbtest.ID(999)); !errors.Is(err, model.ErrSharedVariableNotFound) {
		t.Fatalf("missing get err = %v", err)
	}
	if v, ok, err := r.FindByName(ctx, org1, nil, "SMTP_HOST"); err != nil || !ok || v.ID != plain.ID {
		t.Fatalf("find org = %+v %v %v", v, ok, err)
	}
	if v, ok, err := r.FindByName(ctx, org1, ptr(srv1), "SMTP_HOST"); err != nil || !ok || v.ID != local.ID {
		t.Fatalf("find server = %+v %v %v", v, ok, err)
	}
	if _, ok, err := r.FindByName(ctx, org1, nil, "NOPE"); err != nil || ok {
		t.Fatalf("find missing = %v %v", ok, err)
	}
	if revision(t, d, srv1) != "3" || revision(t, d, srv2) != "1" || revision(t, d, srv3) != "0" {
		t.Fatalf("revisions = %s %s %s", revision(t, d, srv1), revision(t, d, srv2), revision(t, d, srv3))
	}
}

func TestOrgScopeNamesAreUnique(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	save := func(id int, serverID *string) error {
		return r.Save(ctx, model.SharedVariableSave{Create: true, Variable: model.SharedVariable{
			ID: dbtest.ID(id), OrgID: org1, ServerID: serverID, Name: "DUP", Value: "x"}})
	}
	if err := save(201, nil); err != nil {
		t.Fatal(err)
	}
	if err := save(202, nil); err == nil {
		t.Fatal("a second org-scope DUP was accepted (partial unique index missing)")
	}
	if err := save(203, ptr(srv1)); err != nil {
		t.Fatalf("server scope may reuse an org name: %v", err)
	}
	if err := save(204, ptr(srv1)); err == nil {
		t.Fatal("a second DUP on the same server was accepted")
	}
	if err := save(205, ptr(srv2)); err != nil {
		t.Fatalf("another server may reuse the name: %v", err)
	}
}

func TestUpdateReplaceAndGapFill(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	v := model.SharedVariable{ID: dbtest.ID(301), OrgID: org1, Name: "TOKEN", IsSecret: true}
	if err := r.Save(ctx, model.SharedVariableSave{Variable: v, Create: true, Ciphertexts: map[string]string{srv1: "OLD-1"}}); err != nil {
		t.Fatal(err)
	}
	// Gap fill adds a row and keeps the existing one.
	if err := r.Save(ctx, model.SharedVariableSave{Variable: v, Ciphertexts: map[string]string{srv2: "NEW-2"}}); err != nil {
		t.Fatal(err)
	}
	cts, _ := r.CiphertextsForServer(ctx, srv1)
	if cts[v.ID] != "OLD-1" {
		t.Fatalf("gap fill overwrote srv1: %v", cts)
	}
	// A new value replaces every row.
	if err := r.Save(ctx, model.SharedVariableSave{Variable: v, ReplaceCiphertexts: true, Ciphertexts: map[string]string{srv1: "ROT-1"}}); err != nil {
		t.Fatal(err)
	}
	if cts, _ := r.CiphertextsForServer(ctx, srv2); len(cts) != 0 {
		t.Fatalf("replace left srv2's copy: %v", cts)
	}
	if cts, _ := r.CiphertextsForServer(ctx, srv1); cts[v.ID] != "ROT-1" {
		t.Fatalf("replace = %v", cts)
	}
	// A description-only update keeps the ciphertexts.
	v.Description = "rotated monthly"
	if err := r.Save(ctx, model.SharedVariableSave{Variable: v}); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get(ctx, v.ID)
	if got.Description != "rotated monthly" || len(got.CipherServers) != 1 {
		t.Fatalf("after description edit = %+v", got)
	}
}

// A gap fill decided before a concurrent rotation committed must not
// overwrite the rotation's new ciphertext for the same server.
func TestGapFillNeverOverwritesRotation(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	v := model.SharedVariable{ID: dbtest.ID(302), OrgID: org1, Name: "RACE", IsSecret: true}
	if err := r.Save(ctx, model.SharedVariableSave{Variable: v, Create: true, Ciphertexts: map[string]string{srv1: "OLD-1"}}); err != nil {
		t.Fatal(err)
	}
	// The rotation lands first and covers srv2 too.
	if err := r.Save(ctx, model.SharedVariableSave{Variable: v, ReplaceCiphertexts: true,
		Ciphertexts: map[string]string{srv1: "ROT-1", srv2: "ROT-2"}}); err != nil {
		t.Fatal(err)
	}
	// The stale gap fill (srv2 looked missing when it was decided) arrives.
	if err := r.Save(ctx, model.SharedVariableSave{Variable: v, Ciphertexts: map[string]string{srv2: "GAP-2"}}); err != nil {
		t.Fatal(err)
	}
	if cts, _ := r.CiphertextsForServer(ctx, srv2); cts[v.ID] != "ROT-2" {
		t.Fatalf("gap fill overwrote the rotation: %v", cts)
	}
}

func TestDeleteDropPurgeAndCascade(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	a := model.SharedVariable{ID: dbtest.ID(401), OrgID: org1, Name: "A", IsSecret: true}
	b := model.SharedVariable{ID: dbtest.ID(402), OrgID: org1, ServerID: ptr(srv2), Name: "B", IsSecret: true}
	for _, s := range []model.SharedVariableSave{
		{Variable: a, Create: true, Ciphertexts: map[string]string{srv1: "A1", srv2: "A2"}},
		{Variable: b, Create: true, Ciphertexts: map[string]string{srv2: "B2"}},
	} {
		if err := r.Save(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.DropCiphertexts(ctx, srv1, []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	if cts, _ := r.CiphertextsForServer(ctx, srv1); len(cts) != 0 {
		t.Fatalf("drop left %v", cts)
	}
	before := revision(t, d, srv2)
	if err := r.PurgeServerCiphertexts(ctx, srv2); err != nil {
		t.Fatal(err)
	}
	if cts, _ := r.CiphertextsForServer(ctx, srv2); len(cts) != 0 {
		t.Fatalf("purge left %v", cts)
	}
	if revision(t, d, srv2) == before {
		t.Fatal("purge did not bump the revision")
	}
	if err := r.Delete(ctx, a.ID, []string{srv1, srv2}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, a.ID); !errors.Is(err, model.ErrSharedVariableNotFound) {
		t.Fatalf("deleted variable still there: %v", err)
	}
	// Removing a server drops its server-scope variables.
	dbtest.Exec(t, d, `DELETE FROM servers WHERE server_id = ?`, srv2)
	if _, err := r.Get(ctx, b.ID); !errors.Is(err, model.ErrSharedVariableNotFound) {
		t.Fatalf("server-scope variable survived its server: %v", err)
	}
}

func TestServerInfoAndKeys(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	info, err := r.ServerVarsInfo(ctx, srv1)
	if err != nil || info.OrgID != org1 || info.Name != "alpha" || info.Revision != 0 {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if _, err := r.ServerVarsInfo(ctx, dbtest.ID(998)); !errors.Is(err, model.ErrServerNotFound) {
		t.Fatalf("missing server err = %v", err)
	}
	keys, err := r.OrgServerKeys(ctx, org1)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].ServerID != srv1 || keys[0].PublicKey != "KEY-1" || keys[0].Name != "alpha" ||
		keys[1].ServerID != srv2 || keys[1].PublicKey != "" {
		t.Fatalf("keys = %+v", keys)
	}
}

func TestRaiseVarsRevisionNeverLowers(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	if err := r.RaiseVarsRevision(ctx, srv1, 7); err != nil {
		t.Fatal(err)
	}
	if info, _ := r.ServerVarsInfo(ctx, srv1); info.Revision != 7 {
		t.Fatalf("raised to %d, want 7", info.Revision)
	}
	if err := r.RaiseVarsRevision(ctx, srv1, 3); err != nil {
		t.Fatal(err)
	}
	if info, _ := r.ServerVarsInfo(ctx, srv1); info.Revision != 7 {
		t.Fatalf("lowered to %d", info.Revision)
	}
	if info, _ := r.ServerVarsInfo(ctx, srv2); info.Revision != 0 {
		t.Fatalf("another server moved to %d", info.Revision)
	}
}
