// Package repository_test holds checks that span every repository.
package repository_test

import (
	"fmt"
	"testing"
	"time"

	"winterflow/internal/domain/alert"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/dbtest"
	accesstokenrepo "winterflow/internal/infra/db/repository/accesstoken"
	alertrepo "winterflow/internal/infra/db/repository/alert"
	apprepo "winterflow/internal/infra/db/repository/app"
	appdomainrepo "winterflow/internal/infra/db/repository/appdomain"
	enrollmentrepo "winterflow/internal/infra/db/repository/enrollment"
	hubcertrepo "winterflow/internal/infra/db/repository/hubcert"
	hubtokenrepo "winterflow/internal/infra/db/repository/hubtoken"
	metricrepo "winterflow/internal/infra/db/repository/metric"
	rolloutrepo "winterflow/internal/infra/db/repository/rollout"
	serverrepo "winterflow/internal/infra/db/repository/server"
	serverdomainrepo "winterflow/internal/infra/db/repository/serverdomain"
	serverstoragerepo "winterflow/internal/infra/db/repository/serverstorage"
	sharedvarrepo "winterflow/internal/infra/db/repository/sharedvar"
	templaterepo "winterflow/internal/infra/db/repository/template"
	userrepo "winterflow/internal/infra/db/repository/user"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/util"
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
	rollouts := must(rolloutrepo.New(d.Tx, d.Driver, dbtest.Log))

	admin := must(users.BootstrapLocalAdmin(ctx, "Admin", "admin@example.com", "correct horse battery"))
	orgID := must(users.PrimaryOrganizationID(ctx, admin.ID))
	if _, err := users.CreateUser(ctx, dto.UserDTO{Provider: "google", AccountID: "g-1", Name: "G"}); err != nil {
		t.Fatal(err)
	}
	if _, err := users.CreateMemberUser(ctx, orgID, "Member", "member@example.com", "temporary password"); err != nil {
		t.Fatal(err)
	}
	tokens := must(accesstokenrepo.New(d.Tx, d.Driver))
	expires := time.Now().In(zone).Add(24 * time.Hour) // future, non-UTC, sub-second
	if err := tokens.Insert(ctx, model.AccessToken{ID: dbtest.ID(701), UserID: admin.ID, Kind: model.TokenSession,
		Scope: model.ScopeFull, CreatedAt: at, LastUsedAt: at, ExpiresAt: &expires}, "h1"); err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.Touch(ctx, dbtest.ID(701), at.Add(time.Minute), &expires); err != nil {
		t.Fatal(err)
	}
	if err := tokens.Revoke(ctx, dbtest.ID(701), at.Add(time.Hour)); err != nil {
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

	enrollments := must(enrollmentrepo.New(d.Tx, d.Driver))
	if err := enrollments.Create(ctx, model.Enrollment{
		ID: dbtest.ID(401), PollSecretHash: "hash-401", CSR: "csr", Hostname: "box", Code: "ENR401",
		CodeExpiresAt: time.Now().In(zone).Add(time.Hour), CodesIssued: 1, State: model.EnrollmentPending,
		CreatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	if err := enrollments.Create(ctx, model.Enrollment{ // stays pending, keeps its expiry
		ID: dbtest.ID(402), PollSecretHash: "hash-402", CSR: "csr", Hostname: "box2", Code: "ENR402",
		CodeExpiresAt: time.Now().In(zone).Add(time.Hour), CodesIssued: 1, State: model.EnrollmentPending,
		CreatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := enrollments.Claim(ctx, port.ClaimEnrollment{
		EnrollmentID: dbtest.ID(401), Code: "ENR401", ServerID: dbtest.ID(103), OrganizationID: orgID,
		HubID: "hub-01", CertificateID: dbtest.ID(203), Certificate: "PEM",
		CertExpiresAt: at.Add(365 * 24 * time.Hour), Now: time.Now().In(zone),
	}); err != nil {
		t.Fatal(err)
	}

	hubTokens := must(hubtokenrepo.New(d.Tx, d.Driver))
	if err := hubTokens.Create(ctx, "hub-token-hash", time.Now().In(zone).Add(time.Hour), time.Now().In(zone)); err != nil {
		t.Fatal(err)
	}
	if ok, err := hubTokens.Consume(ctx, "hub-token-hash", "hub-01", time.Now().In(zone)); err != nil || !ok {
		t.Fatalf("consume hub token: %v, %v", ok, err)
	}
	hubCerts := must(hubcertrepo.New(d.Tx, d.Driver))
	if err := hubCerts.Trust(ctx, "hub-01", "fp-1", time.Now().In(zone)); err != nil {
		t.Fatal(err)
	}
	if err := hubCerts.Trust(ctx, "hub-01", "fp-2", time.Now().In(zone)); err != nil {
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

	alerts := must(alertrepo.New(d.Tx, d.Driver, dbtest.Log, util.GenerateID))
	channel := must(alerts.CreateChannel(ctx, alert.Channel{OrgID: orgID, Name: "Ops", Service: "ntfy", Enabled: true, URLEncrypted: "x"}))
	if err := alerts.RecordDelivery(ctx, channel.ID, at, ""); err != nil {
		t.Fatal(err)
	}
	rule := must(alerts.CreateRule(ctx, alert.Rule{OrgID: orgID, TargetKind: alert.TargetServer, Metric: alert.MetricCPU,
		Threshold: 90, DurationMinutes: 10, Enabled: true}))
	key := alert.Key{RuleID: rule.ID, Target: alert.Target{ServerID: srv.ID}}
	if _, err := alerts.Fire(ctx, key, 93, 90, alert.Event{ID: util.GenerateID(), OrgID: orgID, RuleID: rule.ID,
		ServerID: srv.ID, TargetKind: alert.TargetServer, Metric: alert.MetricCPU, Kind: alert.EventFired, CreatedAt: at}); err != nil {
		t.Fatal(err)
	}

	if err := sharedVars.Save(ctx, model.SharedVariableSave{Create: true, Variable: model.SharedVariable{
		ID: dbtest.ID(401), OrgID: orgID, Name: "TZ_VAR", Description: "d", UpdatedAt: at,
	}}); err != nil {
		t.Fatal(err)
	}

	tplID := dbtest.ID(501)
	if err := templates.CreateTemplate(ctx, model.Template{ID: tplID, Source: model.TemplateSourceOrg, OrgID: orgID, Name: "shop"},
		model.TemplateRevision{Body: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	// withdrawn_at: a catalog template the feed dropped.
	if err := templates.CreateTemplate(ctx, model.Template{ID: dbtest.ID(503), Source: model.TemplateSourceCatalog, Slug: "gone", Name: "Gone", WithdrawnAt: &at},
		model.TemplateRevision{Body: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	// catalog_feed_at: a catalog template the sync wrote.
	if _, err := templates.ApplyCatalog(ctx, nil, model.CatalogEntry{Slug: "fed", Name: "Fed", Body: []byte(`{}`), Digest: "sha256:x", FeedAt: at}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := templates.ClaimInstance(ctx, model.Instance{
		ID: dbtest.ID(502), OrgID: orgID, Key: "k", AppID: appID, ServerID: srv.ID, TemplateID: tplID, Revision: 1, RequestHash: "h",
	}); err != nil {
		t.Fatal(err)
	}

	// created_at on insert, lease_until on the claim, finished_at when it
	// ends (a second one keeps its lease for the check).
	for i, id := range []string{dbtest.ID(601), dbtest.ID(602)} {
		ro := model.Rollout{ID: id, OrgID: orgID, Kind: model.RolloutTemplateUpgrade, Scope: []byte(`{}`), ScopeKey: fmt.Sprint("tpl-", i)}
		if _, err := rollouts.InsertRollout(ctx, ro); err != nil {
			t.Fatal(err)
		}
		if _, err := rollouts.Claim(ctx, id, "w", at, at.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rollouts.Complete(ctx, dbtest.ID(601), "w", 1); err != nil {
		t.Fatal(err)
	}

	// The OAuth tables have no repository yet: write through the generated
	// queries, which bind types.DateTime like every repository. (Replace
	// with the repositories' own writes as they land.)
	q := sqlitegen.New(d.Tx.Querier(ctx))
	dt := types.DateTime(at)
	revoked := types.DateTime(at.Add(time.Hour))
	userID := admin.ID
	clientID, grantID := dbtest.ID(702), dbtest.ID(703)
	for _, err := range []error{
		q.InsertOAuthClient(ctx, sqlitegen.InsertOAuthClientParams{ID: clientID, Name: "c", RedirectUris: "[]", CreatedAt: dt, LastUsedAt: &dt}),
		q.InsertOAuthCode(ctx, sqlitegen.InsertOAuthCodeParams{CodeHash: "c1", ClientID: clientID, UserID: userID, RedirectUri: "u",
			CodeChallenge: "x", Resource: "r", CreatedAt: dt, ExpiresAt: dt}),
		q.InsertOAuthGrant(ctx, sqlitegen.InsertOAuthGrantParams{ID: grantID, UserID: userID, ClientID: clientID, RefreshTokenHash: "r1",
			RotatedAt: &dt, CreatedAt: dt, LastUsedAt: dt, ExpiresAt: dt, RevokedAt: &revoked}),
		q.InsertOAuthSpentRefreshHash(ctx, sqlitegen.InsertOAuthSpentRefreshHashParams{TokenHash: "r0", GrantID: grantID, SpentAt: dt}),
		q.InsertOAuthLoginState(ctx, sqlitegen.InsertOAuthLoginStateParams{StateHash: "s1", Provider: "google", Nonce: "n", Intent: "login",
			CreatedAt: dt, ExpiresAt: dt}),
		q.InsertOAuthHandoff(ctx, sqlitegen.InsertOAuthHandoffParams{CodeHash: "d1", Kind: "login", UserID: userID, Provider: "google",
			CreatedAt: dt, ExpiresAt: dt}),
		q.InsertIdentity(ctx, sqlitegen.InsertIdentityParams{Provider: "google", ExternalID: "sub", UserID: userID, CreatedAt: dt}),
	} {
		if err != nil {
			t.Fatal(err)
		}
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
