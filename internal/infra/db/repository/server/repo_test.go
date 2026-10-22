package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org1, org2       = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2       = dbtest.ID(11), dbtest.ID(12)
	alice, bob, eve  = dbtest.ID(21), dbtest.ID(22), dbtest.ID(23)
	pendingSrv, cert = dbtest.ID(31), dbtest.ID(32)
)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func mustServers(t *testing.T, r *Repo, userID string) []model.Server {
	t.Helper()
	got, err := r.GetServers(dbtest.Ctx(t), userID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustPending(t *testing.T, r *Repo) (string, bool) {
	t.Helper()
	code, ok, err := r.PendingRegistrationCode(dbtest.Ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	return code, ok
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestGetServerUserIDsReturnsOrgMembers(t *testing.T) {
	repo, d := newRepo(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	dbtest.SeedMember(t, d, alice, org1)
	dbtest.SeedMember(t, d, bob, org1)
	dbtest.SeedMember(t, d, eve, org2)

	got, err := repo.GetServerUserIDs(dbtest.Ctx(t), srv1)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if len(got) != 2 || got[0] != alice || got[1] != bob {
		t.Fatalf("want [alice bob], got %v", got)
	}
}

func TestGetOrgUserIDsReturnsOrgMembers(t *testing.T) {
	repo, d := newRepo(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	dbtest.SeedMember(t, d, alice, org1)
	dbtest.SeedMember(t, d, bob, org1)
	dbtest.SeedMember(t, d, eve, org2)

	got, err := repo.GetOrgUserIDs(dbtest.Ctx(t), org1)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if len(got) != 2 || got[0] != alice || got[1] != bob {
		t.Fatalf("want [alice bob], got %v", got)
	}
	if got, err := repo.GetOrgUserIDs(dbtest.Ctx(t), dbtest.ID(99)); err != nil || len(got) != 0 {
		t.Fatalf("unknown org: want empty, got %v, %v", got, err)
	}
}

func TestGetServerUserIDsUnknownServerIsEmpty(t *testing.T) {
	repo, _ := newRepo(t)
	got, err := repo.GetServerUserIDs(dbtest.Ctx(t), srv1)
	if err != nil || len(got) != 0 {
		t.Fatalf("want empty, got %v, %v", got, err)
	}
}

func TestGetServersIncludesCapabilitiesAndFeatures(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	dbtest.SeedMember(t, d, alice, org1)
	if err := repo.SaveCapabilities(ctx, srv1,
		map[string]string{"server_ip": "203.0.113.7", "system_cpu_cores": "8"}, map[string]bool{"ingress": true}); err != nil {
		t.Fatal(err)
	}
	// Upsert: a second report replaces values.
	if err := repo.SaveCapabilities(ctx, srv1, map[string]string{"system_cpu_cores": "4"}, map[string]bool{"ingress": false}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveCapabilities(ctx, srv2, map[string]string{"server_ip": "198.51.100.1"}, nil); err != nil {
		t.Fatal(err)
	}

	servers, err := repo.GetServers(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].ID != srv1 || servers[0].OrganizationID != org1 || servers[0].LastSeenAt != nil {
		t.Fatalf("servers = %+v", servers)
	}
	caps := servers[0].Capabilities
	if len(caps) != 2 || caps[0].Name != "server_ip" || caps[0].Value != "203.0.113.7" || caps[1].Name != "system_cpu_cores" || caps[1].Value != "4" {
		t.Fatalf("capabilities = %+v", caps)
	}
	if enabled, ok := servers[0].Features["ingress"]; !ok || enabled {
		t.Fatalf("features = %v", servers[0].Features)
	}
	// The JSON the handler sends must expose name/value in lower case.
	raw, err := json.Marshal(servers[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"name":"server_ip"`) || !strings.Contains(string(raw), `"value":"203.0.113.7"`) {
		t.Fatalf("serialized server = %s", raw)
	}
}

// A server with no capabilities or features reports nil for both, so the
// JSON omits them (omitempty), as before.
func TestGetServersWithoutCapabilitiesOmitsThem(t *testing.T) {
	repo, d := newRepo(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedMember(t, d, alice, org1)
	servers, err := repo.GetServers(dbtest.Ctx(t), alice)
	if err != nil || len(servers) != 1 {
		t.Fatalf("servers = %+v, %v", servers, err)
	}
	if servers[0].Capabilities != nil || servers[0].Features != nil {
		t.Fatalf("want nil capabilities/features, got %+v", servers[0])
	}
	if servers := mustServers(t, repo, eve); len(servers) != 0 {
		t.Fatalf("non-member sees %+v", servers)
	}
}

func TestOwnershipAndPresence(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	if has, err := repo.HasAnyServer(ctx); err != nil || has {
		t.Fatalf("HasAnyServer on empty = %v, %v", has, err)
	}
	if _, ok, err := repo.FirstServerID(ctx); ok || err != nil {
		t.Fatalf("FirstServerID on empty = %v, %v", ok, err)
	}
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedMember(t, d, alice, org1)
	dbtest.SeedOrg(t, d, org2)
	dbtest.SeedMember(t, d, eve, org2)

	if has, err := repo.HasAnyServer(ctx); !has || err != nil {
		t.Fatalf("HasAnyServer = %v, %v with a server", has, err)
	}
	if id, ok, err := repo.FirstServerID(ctx); id != srv1 || !ok || err != nil {
		t.Fatalf("FirstServerID = %q, %v, %v", id, ok, err)
	}
	for _, c := range []struct {
		user, server string
		want         bool
	}{{alice, srv1, true}, {eve, srv1, false}, {alice, srv2, false}} {
		if got, err := repo.UserOwnsServer(ctx, c.user, c.server); got != c.want || err != nil {
			t.Errorf("UserOwnsServer(%s, %s) = %v, %v", c.user, c.server, got, err)
		}
	}
	for _, c := range []struct {
		server, org string
		want        bool
	}{{srv1, org1, true}, {srv1, org2, false}, {srv2, org1, false}} {
		if got, err := repo.ServerInOrg(ctx, c.server, c.org); got != c.want || err != nil {
			t.Errorf("ServerInOrg(%s, %s) = %v, %v", c.server, c.org, got, err)
		}
	}
}

func TestTouchLastSeenAndGetCapability(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedMember(t, d, alice, org1)
	if err := repo.TouchLastSeen(ctx, srv1); err != nil {
		t.Fatal(err)
	}
	if err := repo.TouchLastSeen(ctx, srv2); err != nil {
		t.Fatalf("unknown server: %v", err)
	}
	servers := mustServers(t, repo, alice)
	if len(servers) != 1 || servers[0].LastSeenAt == nil || time.Since(*servers[0].LastSeenAt) > time.Minute || servers[0].LastSeenAt.Location() != time.UTC {
		t.Fatalf("last_seen = %v", servers[0].LastSeenAt)
	}
	if _, ok, err := repo.GetCapability(ctx, srv1, "public_key"); ok || err != nil {
		t.Fatalf("missing capability = %v, %v", ok, err)
	}
	if err := repo.SaveCapabilities(ctx, srv1, map[string]string{"public_key": "BASE64"}, nil); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := repo.GetCapability(ctx, srv1, "public_key"); v != "BASE64" || !ok || err != nil {
		t.Fatalf("GetCapability = %q, %v, %v", v, ok, err)
	}
}

func register(t *testing.T, repo *Repo, code string, expires time.Time) {
	t.Helper()
	if err := repo.RegisterServer(dbtest.Ctx(t), dto.ServerRegistrationDTO{
		ServerID: pendingSrv, CertificateID: cert, Hostname: "box", Code: code,
		ExpiresAt: expires, Certificate: []byte("PEM"), CertificateExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterAndClaimServer(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedOrg(t, d, org1)
	dbtest.SeedMember(t, d, alice, org1)

	if _, ok, err := repo.PendingRegistrationCode(ctx); ok || err != nil {
		t.Fatalf("pending on empty = %v, %v", ok, err)
	}
	register(t, repo, "111111", time.Now().Add(time.Hour))
	// Registering the same server again replaces its registration.
	register(t, repo, "222222", time.Now().Add(time.Hour))
	if code, ok, err := repo.PendingRegistrationCode(ctx); code != "222222" || !ok || err != nil {
		t.Fatalf("pending = %q, %v, %v", code, ok, err)
	}
	if got := dbtest.QueryString(t, d, `SELECT certificate FROM server_registrations`); got != base64.StdEncoding.EncodeToString([]byte("PEM")) {
		t.Fatalf("stored certificate = %q", got)
	}

	if _, err := repo.ClaimServer(ctx, dto.ClaimServerDTO{Code: "111111", OrganizationID: org1}); !errors.Is(err, model.ErrInvalidRegistrationCode) {
		t.Fatalf("replaced code: err = %v", err)
	}
	srv, err := repo.ClaimServer(ctx, dto.ClaimServerDTO{Code: "222222", OrganizationID: org1})
	if err != nil {
		t.Fatal(err)
	}
	if srv.ID != pendingSrv || srv.OrganizationID != org1 || srv.Name != "box" || srv.CreatedAt.IsZero() || srv.Capabilities != nil {
		t.Fatalf("claimed = %+v", srv)
	}
	if owns, err := repo.UserOwnsServer(ctx, alice, pendingSrv); !owns || err != nil {
		t.Fatalf("claimed server not owned by the org: %v, %v", owns, err)
	}
	if got := dbtest.QueryString(t, d, `SELECT certificate FROM server_certificates WHERE certificate_id = ?`, cert); got != base64.StdEncoding.EncodeToString([]byte("PEM")) {
		t.Fatalf("certificate = %q", got)
	}
	if _, ok := mustPending(t, repo); ok {
		t.Fatal("registration survived the claim")
	}
}

func TestClaimServerRejectsExpiredCode(t *testing.T) {
	repo, d := newRepo(t)
	dbtest.SeedOrg(t, d, org1)
	register(t, repo, "333333", time.Now().Add(-time.Minute))
	if _, err := repo.ClaimServer(dbtest.Ctx(t), dto.ClaimServerDTO{Code: "333333", OrganizationID: org1}); !errors.Is(err, model.ErrRegistrationCodeExpired) {
		t.Fatalf("err = %v, want ErrRegistrationCodeExpired", err)
	}
}

// A claim that fails half-way (the certificate id is already taken, after
// the server row went in) rolls back: no server, registration kept.
func TestClaimServerIsAtomic(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.Exec(t, d, `INSERT INTO server_certificates (certificate_id, server_id, certificate, expires_at, created_at)
		VALUES (?, ?, 'x', '2030-01-01 00:00:00', '2026-01-01 00:00:00')`, cert, srv1)
	register(t, repo, "444444", time.Now().Add(time.Hour))
	if _, err := repo.ClaimServer(ctx, dto.ClaimServerDTO{Code: "444444", OrganizationID: org1}); err == nil {
		t.Fatal("claim with a taken certificate id succeeded")
	}
	if n := dbtest.QueryString(t, d, `SELECT CAST(count(*) AS TEXT) FROM servers WHERE server_id = ?`, pendingSrv); n != "0" {
		t.Fatal("the server row survived the failed claim")
	}
	if code, ok := mustPending(t, repo); code != "444444" || !ok {
		t.Fatal("failed claim consumed the registration")
	}
}

func TestClearPendingRegistrations(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	register(t, repo, "555555", time.Now().Add(time.Hour))
	if err := repo.ClearPendingRegistrations(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := mustPending(t, repo); ok {
		t.Fatal("registration survived ClearPendingRegistrations")
	}
}

// Replacing a registration is atomic: when the new insert is rejected (its
// code belongs to another server's registration) the delete of the server's
// previous registration rolls back with it.
func TestRegisterServerIsAtomic(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	register(t, repo, "666666", time.Now().Add(time.Hour))
	other := dto.ServerRegistrationDTO{
		ServerID: dbtest.ID(41), CertificateID: dbtest.ID(42), Hostname: "other", Code: "777777",
		ExpiresAt: time.Now().Add(time.Hour), Certificate: []byte("PEM"), CertificateExpiresAt: time.Now().Add(24 * time.Hour),
	}
	if err := repo.RegisterServer(ctx, other); err != nil {
		t.Fatal(err)
	}

	clash := other
	clash.ServerID = pendingSrv // replaces 666666 ...
	if err := repo.RegisterServer(ctx, clash); err == nil {
		t.Fatal("registration with another server's code succeeded") // ... but 777777 is taken
	}
	if got := dbtest.QueryString(t, d, `SELECT code FROM server_registrations WHERE server_id = ?`, pendingSrv); got != "666666" {
		t.Fatalf("previous registration lost after the failed replace, code = %q", got)
	}
}

func TestServerHub(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	dbtest.Exec(t, d, `UPDATE servers SET hub_id = ? WHERE server_id = ?`, "hub-01.winterflow.dev", srv1)

	if got, err := repo.ServerHub(ctx, srv1); err != nil || got != "hub-01.winterflow.dev" {
		t.Fatalf("assigned: %q, %v", got, err)
	}
	if got, err := repo.ServerHub(ctx, srv2); err != nil || got != "" {
		t.Fatalf("no hub: want empty, got %q, %v", got, err)
	}
	if _, err := repo.ServerHub(ctx, dbtest.ID(99)); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown server: want ErrNotFound, got %v", err)
	}
}

func TestCountServersByHub(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	if got, err := repo.CountServersByHub(ctx); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v, %v", got, err)
	}
	for i, hub := range []string{"hub-01", "hub-01", "hub-02", ""} {
		id := dbtest.ID(40 + i)
		dbtest.SeedServer(t, d, dbtest.ID(50+i), id)
		if hub != "" {
			dbtest.Exec(t, d, `UPDATE servers SET hub_id = ? WHERE server_id = ?`, hub, id)
		}
	}
	got, err := repo.CountServersByHub(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["hub-01"] != 2 || got["hub-02"] != 1 {
		t.Fatalf("counts = %v", got)
	}
}

func countRows(t *testing.T, d *db.DB, table, serverID string) string {
	t.Helper()
	return dbtest.QueryString(t, d, `SELECT count(*) FROM `+table+` WHERE server_id = ?`, serverID)
}

func TestDeleteServerCascades(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	app1, app2 := dbtest.ID(41), dbtest.ID(42)
	dbtest.SeedApp(t, d, app1, srv1, "one")
	dbtest.SeedApp(t, d, app2, srv2, "two")
	for _, s := range []string{srv1, srv2} {
		if err := repo.SaveCapabilities(ctx, s, map[string]string{"k": "v"}, map[string]bool{"f": true}); err != nil {
			t.Fatal(err)
		}
	}
	dbtest.Exec(t, d, `INSERT INTO app_domains (domain, app_id, server_id, kind, ssl, upstream_port, target, code, updated_at)
		VALUES ('a.example.com', ?, ?, 'ingress', false, 0, '', 0, '2026-01-01 00:00:00')`, app1, srv1)
	dbtest.Exec(t, d, `INSERT INTO agent_enrollments (enrollment_id, poll_secret_hash, csr, hostname, codes_issued, state, server_id)
		VALUES (?, 'h', 'c', 'box', 1, 'claimed', ?)`, dbtest.ID(51), srv1)

	if err := repo.DeleteServer(ctx, srv1); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"servers", "server_capabilities", "server_features", "apps", "app_domains", "agent_enrollments"} {
		if got := countRows(t, d, table, srv1); got != "0" {
			t.Errorf("%s still has %s rows of the deleted server", table, got)
		}
	}
	// The other server is untouched.
	for _, table := range []string{"servers", "server_capabilities", "apps"} {
		if got := countRows(t, d, table, srv2); got == "0" {
			t.Errorf("%s lost the other server's rows", table)
		}
	}
	// Unknown id is a no-op.
	if err := repo.DeleteServer(ctx, dbtest.ID(99)); err != nil {
		t.Fatal(err)
	}
}

func TestActiveServerCertificate(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedOrg(t, d, org1)
	dbtest.SeedMember(t, d, alice, org1)
	register(t, repo, "111111", time.Now().Add(time.Hour))

	if _, err := repo.ActiveServerCertificate(ctx, pendingSrv); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unclaimed: err = %v", err)
	}
	if _, err := repo.ClaimServer(ctx, dto.ClaimServerDTO{Code: "111111", OrganizationID: org1}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ActiveServerCertificate(ctx, pendingSrv)
	if err != nil || got != base64.StdEncoding.EncodeToString([]byte("PEM")) {
		t.Fatalf("got %q, %v", got, err)
	}
}
