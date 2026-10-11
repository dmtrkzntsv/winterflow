package appdomain

import (
	"context"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org1, org2       = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2       = dbtest.ID(11), dbtest.ID(12)
	app1, app2, app3 = dbtest.ID(21), dbtest.ID(22), dbtest.ID(23)
	appA, appZ       = dbtest.ID(31), dbtest.ID(39) // appA sorts first
	appNew           = dbtest.ID(40)                // never stored
)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func mustReplace(t *testing.T, ctx context.Context, r *Repo, appID, serverID string, ing *model.Ingress) {
	t.Helper()
	if err := r.ReplaceForApp(ctx, appID, serverID, ing); err != nil {
		t.Fatal(err)
	}
}

func mustClaims(t *testing.T, ctx context.Context, r *Repo, serverID string, domains []string, exclude string) []model.DomainClaim {
	t.Helper()
	got, err := r.FindClaims(ctx, serverID, domains, exclude)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustListServer(t *testing.T, ctx context.Context, r *Repo, serverID string) map[string][]model.AppDomainInfo {
	t.Helper()
	got, err := r.ListForServer(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustListApp(t *testing.T, ctx context.Context, r *Repo, serverID, appID string) []string {
	t.Helper()
	got, err := r.ListForApp(ctx, serverID, appID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func ingressOf(domains ...model.IngressDomain) *model.Ingress {
	return &model.Ingress{Domains: domains, Redirects: []model.IngressRedirect{}}
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestReplaceAndFindClaims(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedApp(t, d, app1, srv1, "Ghost")
	dbtest.SeedApp(t, d, app2, srv1, "Blog2")

	ing := &model.Ingress{
		Domains: []model.IngressDomain{{Domain: "blog.example.com", UpstreamPort: 8088, SSL: true}},
		Redirects: []model.IngressRedirect{
			{Domain: "www.example.com", To: "https://blog.example.com", Code: 301, SSL: true},
			// Path rule: must NOT produce a row.
			{Domain: "blog.example.com", Path: "/old/*", To: "https://blog.example.com/new", Code: 302},
		},
	}
	if err := repo.ReplaceForApp(ctx, app1, srv1, ing); err != nil {
		t.Fatal(err)
	}

	// Another app asking for a held domain conflicts, with names attached;
	// a domain listed twice is reported once.
	claims, err := repo.FindClaims(ctx, srv1, []string{"blog.example.com", "free.example.com", "blog.example.com"}, app2)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].Domain != "blog.example.com" || claims[0].AppName != "Ghost" || claims[0].AppID != app1 || claims[0].ServerID != srv1 {
		t.Fatalf("claims = %+v", claims)
	}

	// Claims come back sorted by domain.
	claims = mustClaims(t, ctx, repo, srv1, []string{"www.example.com", "blog.example.com"}, "")
	if len(claims) != 2 || claims[0].Domain != "blog.example.com" || claims[1].Domain != "www.example.com" {
		t.Fatalf("unsorted claims: %+v", claims)
	}

	// The owner re-saving is not a conflict.
	claims = mustClaims(t, ctx, repo, srv1, []string{"blog.example.com"}, app1)
	if len(claims) != 0 {
		t.Fatalf("own claims reported as conflicts: %+v", claims)
	}
	if claims, err := repo.FindClaims(ctx, srv1, nil, app1); claims != nil || err != nil {
		t.Fatalf("no domains = %+v, %v", claims, err)
	}

	// Replace shrinks: dropping the redirect frees www.
	if err := repo.ReplaceForApp(ctx, app1, srv1, ingressOf(model.IngressDomain{Domain: "blog.example.com", UpstreamPort: 8088, SSL: true})); err != nil {
		t.Fatal(err)
	}
	claims = mustClaims(t, ctx, repo, srv1, []string{"www.example.com"}, app2)
	if len(claims) != 0 {
		t.Fatalf("stale row survived replace: %+v", claims)
	}

	// nil ingress = no-op (old client edit must not wipe rows).
	if err := repo.ReplaceForApp(ctx, app1, srv1, nil); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListForServer(ctx, srv1)
	if err != nil || len(list[app1]) != 1 || !list[app1][0].SSL || list[app1][0].Kind != "route" {
		t.Fatalf("ListForServer after nil replace = %+v, %v", list, err)
	}

	// Empty (non-nil) ingress deletes rows.
	if err := repo.ReplaceForApp(ctx, app1, srv1, &model.Ingress{}); err != nil {
		t.Fatal(err)
	}
	list = mustListServer(t, ctx, repo, srv1)
	if len(list[app1]) != 0 {
		t.Fatalf("rows survived empty replace: %+v", list)
	}
}

// A claim whose app row is gone still reports, with an empty name.
func TestFindClaimsWithoutAppRow(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	if err := repo.ReplaceForApp(ctx, app3, srv1, ingressOf(model.IngressDomain{Domain: "orphan.example.com", UpstreamPort: 80})); err != nil {
		t.Fatal(err)
	}
	claims, err := repo.FindClaims(ctx, srv1, []string{"orphan.example.com"}, "")
	if err != nil || len(claims) != 1 || claims[0].AppName != "" || claims[0].AppID != app3 {
		t.Fatalf("claims = %+v, %v", claims, err)
	}
}

// ReplaceForApp is one transaction: a colliding insert keeps the old rows.
func TestReplaceForAppIsAtomic(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedApp(t, d, app1, srv1, "One")
	dbtest.SeedApp(t, d, app2, srv1, "Two")
	mustReplace(t, ctx, repo, app1, srv1, ingressOf(model.IngressDomain{Domain: "one.example.com", UpstreamPort: 81}))
	mustReplace(t, ctx, repo, app2, srv1, ingressOf(model.IngressDomain{Domain: "two.example.com", UpstreamPort: 82}))
	// app1 now asks for app2's hostname: the insert collides on the key.
	err := repo.ReplaceForApp(ctx, app1, srv1, ingressOf(model.IngressDomain{Domain: "two.example.com", UpstreamPort: 81}))
	if err == nil {
		t.Fatal("colliding replace succeeded")
	}
	if got := mustListApp(t, ctx, repo, srv1, app1); len(got) != 1 || got[0] != "one.example.com" {
		t.Fatalf("app1 rows after failed replace = %v", got)
	}
}

func TestDeleteForAppAndReplaceForServer(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedApp(t, d, app1, srv1, "One")
	dbtest.SeedApp(t, d, app2, srv1, "Two")

	mustReplace(t, ctx, repo, app1, srv1, ingressOf(model.IngressDomain{Domain: "one.example.com", UpstreamPort: 81}))
	mustReplace(t, ctx, repo, app2, srv1, ingressOf(model.IngressDomain{Domain: "two.example.com", UpstreamPort: 82}))

	if err := repo.DeleteForApp(ctx, app1); err != nil {
		t.Fatal(err)
	}
	list := mustListServer(t, ctx, repo, srv1)
	if len(list[app1]) != 0 || len(list[app2]) != 1 {
		t.Fatalf("after delete: %+v", list)
	}

	// Reconcile: agent now reports app2 without ingress and app3 with one.
	repApps := []model.App{
		{ID: app2, Name: "Two"},
		{ID: app3, Name: "Three", Ingress: ingressOf(model.IngressDomain{Domain: "three.example.com", UpstreamPort: 83})},
	}
	if err := repo.ReplaceForServer(ctx, srv1, repApps); err != nil {
		t.Fatal(err)
	}
	list = mustListServer(t, ctx, repo, srv1)
	if len(list[app2]) != 0 || len(list[app3]) != 1 || list[app3][0].Domain != "three.example.com" {
		t.Fatalf("after reconcile: %+v", list)
	}
}

// TestReplaceForServerDedupsDuplicateDomain: a concurrent-save race can
// leave two apps on disk both claiming one domain (BuildConfig resolves it
// by sorted AppID, first claim wins). ReplaceForServer must mirror that so
// exactly one row survives and the reconcile still succeeds.
func TestReplaceForServerDedupsDuplicateDomain(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedApp(t, d, appA, srv1, "Alpha")
	dbtest.SeedApp(t, d, appZ, srv1, "Zulu")

	repApps := []model.App{
		{ID: appZ, Name: "Zulu", Ingress: ingressOf(model.IngressDomain{Domain: "dup.example.com", UpstreamPort: 90})},
		{ID: appA, Name: "Alpha", Ingress: ingressOf(model.IngressDomain{Domain: "dup.example.com", UpstreamPort: 91})},
	}
	if err := repo.ReplaceForServer(ctx, srv1, repApps); err != nil {
		t.Fatalf("ReplaceForServer with duplicate domain returned error: %v", err)
	}
	list, err := repo.ListForServer(ctx, srv1)
	if err != nil {
		t.Fatal(err)
	}
	if len(list[appZ]) != 0 {
		t.Fatalf("later app in sort order kept the domain: %+v", list)
	}
	if len(list[appA]) != 1 || list[appA][0].Domain != "dup.example.com" {
		t.Fatalf("sorted-first app did not keep the domain: %+v", list)
	}
}

// TestHostnamesAreUniquePerServer: two servers may index the same hostname
// (DNS round robin); claims are looked up on one server only.
func TestHostnamesAreUniquePerServer(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedServer(t, d, org2, srv2)
	dbtest.SeedApp(t, d, app1, srv1, "One")
	dbtest.SeedApp(t, d, app2, srv2, "Two")
	shared := ingressOf(model.IngressDomain{Domain: "shop.example.com", UpstreamPort: 80})
	if err := repo.ReplaceForApp(ctx, app1, srv1, shared); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceForApp(ctx, app2, srv2, shared); err != nil {
		t.Fatalf("second server could not index the same hostname: %v", err)
	}
	for server, owner := range map[string]string{srv1: "One", srv2: "Two"} {
		claims, err := repo.FindClaims(ctx, server, []string{"shop.example.com"}, appNew)
		if err != nil || len(claims) != 1 || claims[0].AppName != owner || claims[0].ServerID != server {
			t.Fatalf("%s claims = %+v, %v", server, claims, err)
		}
	}
	// A reconcile of one server leaves the other's row alone.
	if err := repo.ReplaceForServer(ctx, srv1, nil); err != nil {
		t.Fatal(err)
	}
	if claims := mustClaims(t, ctx, repo, srv2, []string{"shop.example.com"}, ""); len(claims) != 1 {
		t.Fatalf("srv2 row lost: %+v", claims)
	}
}

func TestListForApp(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.SeedApp(t, d, app1, srv1, "Ghost")
	dbtest.SeedApp(t, d, app2, srv1, "Blog")
	ing := &model.Ingress{
		Domains:   []model.IngressDomain{{Domain: "blog.example.com", UpstreamPort: 1}},
		Redirects: []model.IngressRedirect{{Domain: "www.example.com", To: "https://blog.example.com", Code: 301}},
	}
	if err := repo.ReplaceForApp(ctx, app1, srv1, ing); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceForApp(ctx, app2, srv1, ingressOf(model.IngressDomain{Domain: "other.example.com", UpstreamPort: 2})); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ListForApp(ctx, srv1, app1)
	if err != nil || len(got) != 2 || got[0] != "blog.example.com" || got[1] != "www.example.com" {
		t.Fatalf("ListForApp = %v, %v", got, err)
	}
	if got := mustListApp(t, ctx, repo, srv2, app1); len(got) != 0 {
		t.Fatalf("another server's rows: %v", got)
	}
}

// The stored row keeps every column the Bun model wrote.
func TestStoredRedirectRow(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	ing := &model.Ingress{Redirects: []model.IngressRedirect{{Domain: "www.example.com", To: "https://x.example.com", Code: 308, SSL: true}}}
	if err := repo.ReplaceForApp(ctx, app1, srv1, ing); err != nil {
		t.Fatal(err)
	}
	got := dbtest.QueryString(t, d, `SELECT kind || '|' || target || '|' || CAST(code AS TEXT) || '|' || CAST(upstream_port AS TEXT) FROM app_domains WHERE domain = 'www.example.com'`)
	if got != "redirect|https://x.example.com|308|0" {
		t.Fatalf("stored redirect row = %q", got)
	}
}
