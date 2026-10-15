package template

import (
	"errors"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/dbtest"
)

var cat1, cat2 = dbtest.ID(31), dbtest.ID(32)

// mustCatalog inserts a catalog template (revision 1 with digest d).
func mustCatalog(t *testing.T, r *Repo, id, slug, name string, withdrawn *time.Time) {
	t.Helper()
	err := r.CreateTemplate(dbtest.Ctx(t), model.Template{ID: id, Source: model.TemplateSourceCatalog, Slug: slug, Name: name, WithdrawnAt: withdrawn},
		model.TemplateRevision{Body: []byte(`{"files":{}}`), Digest: "sha256:" + slug})
	if err != nil {
		t.Fatal(err)
	}
}

// Every org reads every catalog template; only its own templates change.
func TestCatalogTemplatesAreReadByEveryOrg(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "zeta")
	mustCatalog(t, r, cat1, "actual-budget", "Actual Budget", nil)
	for _, org := range []string{org1, org2} {
		got, err := r.GetTemplate(ctx, org, cat1)
		if err != nil || !got.Catalog() || got.OrgID != "" || got.Slug != "actual-budget" || got.WithdrawnAt != nil {
			t.Fatalf("%s reads %+v, %v", org, got, err)
		}
	}
	list, err := r.ListTemplates(ctx, org1)
	if err != nil || len(list) != 2 || list[0].ID != tpl1 || list[1].ID != cat1 || list[1].Source != model.TemplateSourceCatalog {
		t.Fatalf("org1 list (its own first) = %+v, %v", list, err)
	}
	names, err := r.ListTemplateNames(ctx, org2)
	if err != nil || len(names) != 1 || names[cat1] != "Actual Budget" {
		t.Fatalf("org2 names = %+v, %v", names, err)
	}
	rev, err := r.GetRevision(ctx, cat1, 1)
	if err != nil || rev.Digest != "sha256:actual-budget" {
		t.Fatalf("revision = %+v, %v", rev, err)
	}
	if _, err := r.AddRevision(ctx, org1, cat1, "Actual Budget", 1, model.TemplateRevision{Body: []byte(`{}`)}); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("an org adds a catalog revision: %v", err)
	}
	if err := r.DeleteTemplate(ctx, org1, cat1); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("an org deletes a catalog template: %v", err)
	}
	err = r.CreateTemplate(ctx, model.Template{ID: cat2, Source: model.TemplateSourceCatalog, Slug: "actual-budget", Name: "Other"},
		model.TemplateRevision{Body: []byte(`{}`)})
	if !errors.Is(err, model.ErrTemplateNameTaken) {
		t.Fatalf("a taken slug: %v", err)
	}
	if err := r.CreateTemplate(ctx, model.Template{ID: cat2, Source: "mine", Name: "x"}, model.TemplateRevision{}); err == nil {
		t.Fatal("an unknown source was stored")
	}
}

// A withdrawn catalog template is listed only to an org that still has
// live instances of it; it stays readable to every org.
func TestWithdrawnCatalogTemplatesListOnlyWithInstances(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	mustCatalog(t, r, cat1, "gone", "Gone", &at)
	inst := instance(1, "k")
	inst.TemplateID = cat1
	if _, _, err := r.ClaimInstance(ctx, inst); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.ListTemplates(ctx, org1); len(list) != 1 || list[0].Instances != 1 || list[0].WithdrawnAt == nil || !list[0].WithdrawnAt.Equal(at) {
		t.Fatalf("org1 (an instance) list = %+v", list)
	}
	if list, _ := r.ListTemplates(ctx, org2); len(list) != 0 {
		t.Fatalf("org2 (no instance) list = %+v", list)
	}
	if got, err := r.GetTemplate(ctx, org2, cat1); err != nil || got.WithdrawnAt == nil {
		t.Fatalf("org2 reads %+v, %v", got, err)
	}
}

// An instance's name and hosts round-trip through the claim; a confirmed
// save replaces the hosts.
func TestInstanceNameAndHostsRoundTrip(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	inst := instance(1, "k")
	inst.Name = "My shop"
	inst.Hosts = []model.InstanceHost{{Host: "a.example.test", Expose: "app:8080"}}
	got, _, err := r.ClaimInstance(ctx, inst)
	if err != nil || got.Name != "My shop" || len(got.Hosts) != 1 || got.Hosts[0] != inst.Hosts[0] {
		t.Fatalf("claimed = %+v, %v", got, err)
	}
	if err := r.MarkSaved(ctx, got.ID, 1, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.GetInstance(ctx, org1, "k"); got.Hosts == nil || len(got.Hosts) != 0 {
		t.Fatalf("after a save without hosts = %+v", got.Hosts)
	}
}
