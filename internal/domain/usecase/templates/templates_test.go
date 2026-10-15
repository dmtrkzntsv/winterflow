package templates_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
	templaterepo "winterflow/internal/infra/db/repository/template"
)

func TestSaveTemplateConflictsAndWarnings(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: body(), BaseRevision: 0}); !errors.Is(err, model.ErrRevisionConflict) {
		t.Fatalf("stale base: %v", err)
	}
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{Name: "x", Body: body(), BaseRevision: 3}); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("a new template with a base: %v", err)
	}
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{Name: "Bad Name", Body: body()}); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("a bad name: %v", err)
	}
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{Name: "x", Body: body(), Note: strings.Repeat("n", 1001)}); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("a long note: %v", err)
	}
	b := body()
	b.Files["compose.yaml"] += "# ${UNSET}\n"
	saved, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: b, BaseRevision: 1, Note: "two"})
	if err != nil || saved.Revision != 2 || len(saved.Warnings) != 1 || !strings.HasPrefix(saved.Warnings[0], "UNSET is not a declared variable") {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	view, err := f.uc.GetTemplate(ctx, org1, f.tpl.ID, 1)
	if err != nil || view.Revision != 1 || view.LatestRevision != 2 || len(view.Revisions) != 2 || view.Revisions[0].Note != "two" ||
		view.Source != model.TemplateSourceOrg || view.Body.Summary != "A shop" {
		t.Fatalf("view = %+v, %v", view, err)
	}
	if d := view.Detected; len(d.Variables) != 3 || len(d.Expose) != 1 || d.Expose[0].Target != "app:8080" || len(d.Backups) != 1 {
		t.Fatalf("detected = %+v", d)
	}
	if latest, _ := f.uc.GetTemplate(ctx, org1, f.tpl.ID, 0); latest.Revision != 2 || latest.Note != "two" {
		t.Fatalf("latest = %+v", latest)
	}
	if _, err := f.uc.GetTemplate(ctx, org1, f.tpl.ID, 9); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing revision: %v", err)
	}
	list, err := f.uc.ListTemplates(ctx, org1)
	if err != nil || len(list.Templates) != 1 || list.Templates[0].LatestRevision != 2 || list.Templates[0].Source != model.TemplateSourceOrg {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := f.uc.DeleteTemplate(ctx, org2, f.tpl.ID); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("delete from another org: %v", err)
	}
	if err := f.uc.DeleteTemplate(ctx, org1, f.tpl.ID); err != nil {
		t.Fatal(err)
	}
}

func TestValidID(t *testing.T) {
	if !templates.ValidID(dbtest.ID(1)) {
		t.Fatal("a canonical uuid was refused")
	}
	for _, bad := range []string{"", "x", "00000000-0000-7000-8000-00000000000G", "00000000000070008000000000000001", "{00000000-0000-7000-8000-000000000001}"} {
		if templates.ValidID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

// failingShared is a shared variables index that cannot be read.
type failingShared struct{}

func (failingShared) ListOrg(context.Context, string) ([]model.SharedVariable, error) {
	return nil, errors.New("db down")
}

// The warnings are computed before the write: a save never answers an
// error after it stored the revision.
func TestSaveTemplateWarningsBeforeTheWrite(t *testing.T) {
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	repo, err := templaterepo.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	uc := templates.New(templates.Deps{Repo: repo, Shared: failingShared{}, Log: dbtest.Log})
	ctx := dbtest.Ctx(t)
	if _, err := uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{Name: "shop", Body: body()}); err == nil {
		t.Fatal("a failed shared variables read was not reported")
	}
	if list, err := uc.ListTemplates(ctx, org1); err != nil || len(list.Templates) != 0 {
		t.Fatalf("the failed save stored %+v, %v", list, err)
	}
}

// Every org reads a catalog template; none may save or delete it.
func TestCatalogTemplatesAreReadOnly(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	raw, err := template.Encode(body())
	if err != nil {
		t.Fatal(err)
	}
	cat := dbtest.ID(41)
	if err := f.repo.CreateTemplate(ctx, model.Template{ID: cat, Source: model.TemplateSourceCatalog, Slug: "shop", Name: "Shop"},
		model.TemplateRevision{Body: raw, Digest: "sha256:x"}); err != nil {
		t.Fatal(err)
	}
	for _, org := range []string{org1, org2} {
		v, err := f.uc.GetTemplate(ctx, org, cat, 0)
		if err != nil || v.Source != model.TemplateSourceCatalog || v.Slug != "shop" || len(v.Detected.Variables) != 3 {
			t.Fatalf("%s reads %+v, %v", org, v, err)
		}
	}
	if list, _ := f.uc.ListTemplates(ctx, org1); len(list.Templates) != 2 || list.Templates[1].ID != cat {
		t.Fatalf("org1 list = %+v", list)
	}
	_, err = f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: cat, Name: "shop", Body: body(), BaseRevision: 1})
	if !errors.Is(err, model.ErrCatalogReadOnly) || !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("save a catalog template: %v", err)
	}
	if err := f.uc.DeleteTemplate(ctx, org2, cat); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("delete a catalog template: %v", err)
	}
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: dbtest.ID(99), Name: "shop", Body: body(), BaseRevision: 1}); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("save an unknown template: %v", err)
	}
}

// Inspect reads a body as a save would, without storing it: what it
// declares as far as it parses, and the first problem.
func TestInspectTemplate(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	got, err := f.uc.InspectTemplate(ctx, org1, body())
	if err != nil || got.Problem != "" || len(got.Detected.Variables) != 3 || len(got.Detected.Warnings) != 0 {
		t.Fatalf("a good body = %+v, %v", got, err)
	}
	b := body()
	b.Summary = "two\nlines"
	got, err = f.uc.InspectTemplate(ctx, org1, b)
	if err != nil || !strings.Contains(got.Problem, "summary") || len(got.Detected.Variables) != 3 {
		t.Fatalf("a bad summary still shows the block: %+v, %v", got, err)
	}
	b = body()
	b.Files["compose.yaml"] += "  bogus: 1\n"
	got, err = f.uc.InspectTemplate(ctx, org1, b)
	if err != nil || !strings.Contains(got.Problem, "x-winterflow.bogus") || len(got.Detected.Variables) != 0 {
		t.Fatalf("a bad block = %+v, %v", got, err)
	}
	if list, _ := f.uc.ListTemplates(ctx, org1); len(list.Templates) != 1 {
		t.Fatalf("inspect stored something: %+v", list)
	}
	if _, err := templates.New(templates.Deps{Repo: f.repo, Shared: failingShared{}, Log: dbtest.Log}).InspectTemplate(ctx, org1, body()); err == nil {
		t.Fatal("a failed shared variables read was not reported")
	}
}

// A stored body a later parser refuses still reads (its problem shown as a
// warning), but nothing is rendered from it.
func TestARevisionThatNoLongerParses(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	cat := dbtest.ID(41)
	raw := []byte(`{"files":{"compose.yaml":"services: {app: {image: x}}\nx-winterflow: {bogus: 1}\n"}}`)
	if err := f.repo.CreateTemplate(ctx, model.Template{ID: cat, Source: model.TemplateSourceCatalog, Slug: "old", Name: "Old"},
		model.TemplateRevision{Body: raw}); err != nil {
		t.Fatal(err)
	}
	v, err := f.uc.GetTemplate(ctx, org1, cat, 0)
	if err != nil || len(v.Detected.Warnings) != 1 || !strings.Contains(v.Detected.Warnings[0], "x-winterflow.bogus") {
		t.Fatalf("view = %+v, %v", v, err)
	}
	in := f.create(nil)
	in.TemplateID = cat
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, in); err == nil || !strings.Contains(err.Error(), "x-winterflow.bogus") || f.disp.count() != 0 {
		t.Fatalf("a create from it: %v", err)
	}
}
