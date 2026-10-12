// Package repository_test holds checks that span every repository.
package repository_test

import (
	"fmt"
	"testing"
	"time"

	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/dbtest"
	apprepo "winterflow/internal/infra/db/repository/app"
	appdomainrepo "winterflow/internal/infra/db/repository/appdomain"
	metricrepo "winterflow/internal/infra/db/repository/metric"
	serverrepo "winterflow/internal/infra/db/repository/server"
	serverdomainrepo "winterflow/internal/infra/db/repository/serverdomain"
	serverstoragerepo "winterflow/internal/infra/db/repository/serverstorage"
	sharedvarrepo "winterflow/internal/infra/db/repository/sharedvar"
	templaterepo "winterflow/internal/infra/db/repository/template"
	userrepo "winterflow/internal/infra/db/repository/user"
)

// sqliteDateTime matches the only timestamp format SQLite may hold:
// naive UTC "YYYY-MM-DD HH:MM:SS" (types.DateTime.Value, CURRENT_TIMESTAMP).
const sqliteDateTime = "[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9] [0-9][0-9]:[0-9][0-9]:[0-9][0-9]"

// TestSQLiteTimestampsAreDateTimeText writes through every repository with
// non-UTC, sub-second times, then checks every TIMESTAMP column of every
// table: each value must be TEXT in exactly the YYYY-MM-DD HH:MM:SS shape, and
// each column must have been written at least once (so a new timestamp
// column without a write here fails instead of passing vacuously).
func TestSQLiteTimestampsAreDateTimeText(t *testing.T) {
	d := dbtest.Open(t)
	if d.Driver != backend.SQLite {
		t.Skip("SQLite storage format only")
	}
	ctx := dbtest.Ctx(t)
	zone := time.FixedZone("UTC+5:30", 5*3600+30*60)
	at := time.Date(2026, 7, 2, 15, 4, 5, 123456789, zone)

	users := must(userrepo.New(d.Tx, d.Driver, dbtest.Log))
	servers := must(serverrepo.New(d.Tx, d.Driver, dbtest.Log))
	apps := must(apprepo.New(d.Tx, d.Driver, dbtest.Log))
	appDomains := must(appdomainrepo.New(d.Tx, d.Driver, dbtest.Log))
	serverDomains := must(serverdomainrepo.New(d.Tx, d.Driver, dbtest.Log))
	serverStorages := must(serverstoragerepo.New(d.Tx, d.Driver, dbtest.Log))
	metricSamples := must(metricrepo.New(d.Tx, d.Driver, dbtest.Log))
	sharedVars := must(sharedvarrepo.New(d.Tx, d.Driver, dbtest.Log))
	templates := must(templaterepo.New(d.Tx, d.Driver, dbtest.Log))

	admin := must(users.BootstrapLocalAdmin(ctx, "Admin", "admin@example.com", "correct horse battery"))
	orgID := must(users.PrimaryOrganizationID(ctx, admin.ID))
	if _, err := users.CreateUser(ctx, dto.UserDTO{Provider: "google", AccountID: "g-1", Name: "G"}); err != nil {
		t.Fatal(err)
	}
	if _, err := users.CreateMemberUser(ctx, orgID, "Member", "member@example.com", "temporary password"); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().In(zone).Add(24 * time.Hour) // future, non-UTC, sub-second
	_, plaintext, err := users.CreateToken(ctx, admin.ID, "ci", &expires)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.FindByToken(ctx, plaintext); err != nil { // stamps last_used_at
		t.Fatal(err)
	}

	register := func(serverID, certID, code string) {
		t.Helper()
		if err := servers.RegisterServer(ctx, dto.ServerRegistrationDTO{
			ServerID: serverID, CertificateID: certID, Hostname: "box", Code: code,
			ExpiresAt: time.Now().In(zone).Add(time.Hour), Certificate: []byte("PEM"),
			CertificateExpiresAt: at.Add(365 * 24 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	register(dbtest.ID(101), dbtest.ID(201), "111111")
	register(dbtest.ID(102), dbtest.ID(202), "222222") // stays pending
	srv := must(servers.ClaimServer(ctx, dto.ClaimServerDTO{Code: "111111", OrganizationID: orgID}))
	if err := servers.TouchLastSeen(ctx, srv.ID); err != nil {
		t.Fatal(err)
	}
	if err := servers.SaveCapabilities(ctx, srv.ID, map[string]string{"version": "1"}, map[string]bool{"ingress": true}); err != nil {
		t.Fatal(err)
	}

	appID := dbtest.ID(301)
	if err := apps.SaveApp(ctx, model.App{ID: appID, ServerID: srv.ID, Name: "a", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := appDomains.ReplaceForApp(ctx, appID, srv.ID, &model.Ingress{
		Domains: []model.IngressDomain{{Domain: "a.example.com", UpstreamPort: 8080}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := serverDomains.Upsert(ctx, srv.ID, model.RegisteredDomain{
		Name: "example.com", TLS: model.DomainTLSCertificate, NotAfter: &expires, Issuer: "CA",
	}); err != nil {
		t.Fatal(err)
	}

	if err := serverStorages.Upsert(ctx, srv.ID, model.StorageSummary{
		Name: "disk", Type: model.StorageLocal, Location: "/mnt/usb/winterflow",
	}); err != nil {
		t.Fatal(err)
	}

	if err := metricSamples.InsertSample(ctx, srv.ID, at, metrics.Values{"cpu": 1}, nil); err != nil {
		t.Fatal(err)
	}

	if err := sharedVars.Save(ctx, model.SharedVariableSave{Create: true, Variable: model.SharedVariable{
		ID: dbtest.ID(401), OrgID: orgID, Name: "TZ_VAR", Description: "d", UpdatedAt: at,
	}}); err != nil {
		t.Fatal(err)
	}

	tplID := dbtest.ID(501)
	if err := templates.CreateTemplate(ctx, model.Template{ID: tplID, OrgID: orgID, Name: "shop"},
		model.TemplateRevision{Body: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := templates.ClaimInstance(ctx, model.Instance{
		ID: dbtest.ID(502), OrgID: orgID, Key: "k", AppID: appID, ServerID: srv.ID, TemplateID: tplID, Revision: 1, RequestHash: "h",
	}); err != nil {
		t.Fatal(err)
	}

	columns := timestampColumns(t, d)
	if len(columns) == 0 {
		t.Fatal("found no TIMESTAMP columns")
	}
	for _, c := range columns {
		written := dbtest.QueryString(t, d, fmt.Sprintf(
			`SELECT COUNT(*) FROM %q WHERE %q IS NOT NULL`, c.table, c.column))
		if written == "0" {
			t.Errorf("%s.%s: never written by this test; add a write for it", c.table, c.column)
			continue
		}
		bad := dbtest.QueryString(t, d, fmt.Sprintf(
			`SELECT COALESCE(MIN(typeof(%[2]q) || ':' || CAST(%[2]q AS TEXT)), '') FROM %[1]q
			 WHERE %[2]q IS NOT NULL AND (typeof(%[2]q) <> 'text' OR %[2]q NOT GLOB '%[3]s' OR length(%[2]q) <> 19)`,
			c.table, c.column, sqliteDateTime))
		if bad != "" {
			t.Errorf("%s.%s holds %s, want YYYY-MM-DD HH:MM:SS text", c.table, c.column, bad)
		}
	}
}

type tableColumn struct{ table, column string }

// timestampColumns lists every column declared TIMESTAMP or DATETIME.
func timestampColumns(t *testing.T, d *db.DB) []tableColumn {
	t.Helper()
	rows, err := d.SQL.QueryContext(dbtest.Ctx(t), `
		SELECT m.name, p.name FROM sqlite_master m, pragma_table_info(m.name) p
		WHERE m.type = 'table' AND upper(p.type) IN ('TIMESTAMP', 'TIMESTAMPTZ', 'DATETIME')
		ORDER BY m.name, p.name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []tableColumn
	for rows.Next() {
		var c tableColumn
		if err := rows.Scan(&c.table, &c.column); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
