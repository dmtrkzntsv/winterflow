package templates_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
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
	if err != nil || saved.Revision != 2 || len(saved.Warnings) != 1 || !strings.HasPrefix(saved.Warnings[0], "UNSET ") {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	view, err := f.uc.GetTemplate(ctx, org1, f.tpl.ID, 1)
	if err != nil || view.Revision != 1 || view.LatestRevision != 2 || len(view.Revisions) != 2 || view.Revisions[0].Note != "two" {
		t.Fatalf("view = %+v, %v", view, err)
	}
	if latest, _ := f.uc.GetTemplate(ctx, org1, f.tpl.ID, 0); latest.Revision != 2 || latest.Note != "two" {
		t.Fatalf("latest = %+v", latest)
	}
	if _, err := f.uc.GetTemplate(ctx, org1, f.tpl.ID, 9); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing revision: %v", err)
	}
	list, err := f.uc.ListTemplates(ctx, org1)
	if err != nil || len(list.Templates) != 1 || list.Templates[0].LatestRevision != 2 {
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
