package repository

import (
	"context"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

func newDomainRepo(t *testing.T) (*DbAppDomainRepository, *DbAppRepository) {
	t.Helper()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	conn := newTestDB(t)
	seedOrgWithServer(t, conn, "org-1", "srv-1")
	return NewDbAppDomainRepository(conn, log), NewDbAppRepository(conn, log)
}

func seedApp(t *testing.T, apps *DbAppRepository, id, name string) {
	t.Helper()
	if err := apps.SaveApp(context.Background(), model.App{ID: id, ServerID: "srv-1", Name: name, Icon: "i", Color: "#000000"}); err != nil {
		t.Fatal(err)
	}
}

func ingressOf(domains ...model.IngressDomain) *model.Ingress {
	return &model.Ingress{Domains: domains, Redirects: []model.IngressRedirect{}}
}

func TestReplaceAndFindClaims(t *testing.T) {
	repo, apps := newDomainRepo(t)
	ctx := context.Background()
	seedApp(t, apps, "app-1", "Ghost")
	seedApp(t, apps, "app-2", "Blog2")

	ing := &model.Ingress{
		Domains: []model.IngressDomain{{Domain: "blog.example.com", UpstreamPort: 8088, SSL: true}},
		Redirects: []model.IngressRedirect{
			{Domain: "www.example.com", To: "https://blog.example.com", Code: 301, SSL: true},
			// Path rule: must NOT produce a row.
			{Domain: "blog.example.com", Path: "/old/*", To: "https://blog.example.com/new", Code: 302},
		},
	}
	if err := repo.ReplaceForApp(ctx, "app-1", "srv-1", ing); err != nil {
		t.Fatal(err)
	}

	// Another app asking for a held domain conflicts, with names attached.
	claims, err := repo.FindClaims(ctx, []string{"blog.example.com", "free.example.com"}, "app-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].Domain != "blog.example.com" || claims[0].AppName != "Ghost" || claims[0].ServerName == "" {
		t.Fatalf("claims = %+v", claims)
	}

	// The owner re-saving is not a conflict.
	claims, _ = repo.FindClaims(ctx, []string{"blog.example.com"}, "app-1")
	if len(claims) != 0 {
		t.Fatalf("own claims reported as conflicts: %+v", claims)
	}

	// Replace shrinks: dropping the redirect frees www.
	if err := repo.ReplaceForApp(ctx, "app-1", "srv-1", ingressOf(model.IngressDomain{Domain: "blog.example.com", UpstreamPort: 8088, SSL: true})); err != nil {
		t.Fatal(err)
	}
	claims, _ = repo.FindClaims(ctx, []string{"www.example.com"}, "app-2")
	if len(claims) != 0 {
		t.Fatalf("stale row survived replace: %+v", claims)
	}

	// nil ingress = no-op (old client edit must not wipe rows).
	if err := repo.ReplaceForApp(ctx, "app-1", "srv-1", nil); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListForServer(ctx, "srv-1")
	if err != nil || len(list["app-1"]) != 1 {
		t.Fatalf("ListForServer after nil replace = %+v, %v", list, err)
	}

	// Empty (non-nil) ingress deletes rows.
	if err := repo.ReplaceForApp(ctx, "app-1", "srv-1", &model.Ingress{}); err != nil {
		t.Fatal(err)
	}
	list, _ = repo.ListForServer(ctx, "srv-1")
	if len(list["app-1"]) != 0 {
		t.Fatalf("rows survived empty replace: %+v", list)
	}
}

func TestDeleteForAppAndReplaceForServer(t *testing.T) {
	repo, apps := newDomainRepo(t)
	ctx := context.Background()
	seedApp(t, apps, "app-1", "One")
	seedApp(t, apps, "app-2", "Two")

	_ = repo.ReplaceForApp(ctx, "app-1", "srv-1", ingressOf(model.IngressDomain{Domain: "one.example.com", UpstreamPort: 81}))
	_ = repo.ReplaceForApp(ctx, "app-2", "srv-1", ingressOf(model.IngressDomain{Domain: "two.example.com", UpstreamPort: 82}))

	if err := repo.DeleteForApp(ctx, "app-1"); err != nil {
		t.Fatal(err)
	}
	list, _ := repo.ListForServer(ctx, "srv-1")
	if len(list["app-1"]) != 0 || len(list["app-2"]) != 1 {
		t.Fatalf("after delete: %+v", list)
	}

	// Reconcile: agent now reports app-2 without ingress and app-3 with one.
	repApps := []model.App{
		{ID: "app-2", Name: "Two"},
		{ID: "app-3", Name: "Three", Ingress: ingressOf(model.IngressDomain{Domain: "three.example.com", UpstreamPort: 83})},
	}
	if err := repo.ReplaceForServer(ctx, "srv-1", repApps); err != nil {
		t.Fatal(err)
	}
	list, _ = repo.ListForServer(ctx, "srv-1")
	if len(list["app-2"]) != 0 || len(list["app-3"]) != 1 || list["app-3"][0].Domain != "three.example.com" {
		t.Fatalf("after reconcile: %+v", list)
	}
}

// TestReplaceForServerDedupsDuplicateDomain covers the reconcile-can't-heal
// bug: a concurrent-save race can leave two apps on disk both claiming the
// same domain (BuildConfig in internal/infra/ingress/caddy/config.go accepts
// this and resolves it by sorted-AppID, first claim wins). Without dedup here
// the bulk INSERT collides on the domain PK, RunInTx rolls back, and the
// stale index can never self-heal. ReplaceForServer must mirror the agent's
// resolution so exactly one row survives and the call still succeeds.
func TestReplaceForServerDedupsDuplicateDomain(t *testing.T) {
	repo, apps := newDomainRepo(t)
	ctx := context.Background()
	seedApp(t, apps, "app-a", "Alpha")
	seedApp(t, apps, "app-z", "Zulu")

	// Both apps claim "dup.example.com". Sorted by app ID, "app-a" sorts
	// first, so it must be the one that keeps the domain.
	repApps := []model.App{
		{ID: "app-z", Name: "Zulu", Ingress: ingressOf(model.IngressDomain{Domain: "dup.example.com", UpstreamPort: 90})},
		{ID: "app-a", Name: "Alpha", Ingress: ingressOf(model.IngressDomain{Domain: "dup.example.com", UpstreamPort: 91})},
	}
	if err := repo.ReplaceForServer(ctx, "srv-1", repApps); err != nil {
		t.Fatalf("ReplaceForServer with duplicate domain returned error: %v", err)
	}

	list, err := repo.ListForServer(ctx, "srv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list["app-z"]) != 0 {
		t.Fatalf("later app in sort order kept the domain: %+v", list)
	}
	if len(list["app-a"]) != 1 || list["app-a"][0].Domain != "dup.example.com" {
		t.Fatalf("sorted-first app did not keep the domain: %+v", list)
	}
}
