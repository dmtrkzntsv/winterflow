package rollouts

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/template"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
	apprepo "winterflow/internal/infra/db/repository/app"
)

// shopBody is template "shop" declaring a required NAME with pattern.
func shopBody(pattern string) template.Body {
	compose := "services:\n  app:\n    image: example/shop:1\n    environment:\n      - NAME\n" +
		"x-winterflow:\n  variables:\n    NAME: { required: true, pattern: \"" + pattern + "\" }\n"
	return template.Body{Files: map[string]string{"compose.yaml": compose}}
}

// shop saves template "shop" (r1) with created instances named names on
// srv1 and returns it with the instances' app ids by name.
func (e *env) shop(t *testing.T, tu *templates.UseCase, names ...string) (templates.SavedTemplate, map[string]string) {
	t.Helper()
	ctx := dbtest.Ctx(t)
	tpl, err := tu.SaveTemplate(ctx, "u1", org1, templates.SaveInput{Name: "shop", Body: shopBody("^[a-z]+$")})
	if err != nil {
		t.Fatal(err)
	}
	apps := map[string]string{}
	for _, n := range names {
		out, err := tu.CreateInstance(ctx, "u1", org1, templates.CreateInput{
			ServerID: srv1, Key: n, TemplateID: tpl.ID, Name: "shop-" + n, Values: map[string]string{"NAME": n},
		})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case res := <-out.Done:
			if !res.Success {
				t.Fatalf("create %s: %s", n, res.Error)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("create %s: no result", n)
		}
		v, _ := out.Final(ctx)
		apps[n] = v.AppID
	}
	return tpl, apps
}

// revise adds revision 2 with NAME's pattern.
func revise(t *testing.T, tu *templates.UseCase, tpl templates.SavedTemplate, pattern string) {
	t.Helper()
	if _, err := tu.SaveTemplate(dbtest.Ctx(t), "u1", org1, templates.SaveInput{ID: tpl.ID, Name: "shop", Body: shopBody(pattern), BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
}

// revisions are the instances' revisions by key.
func revisions(t *testing.T, e *env) map[string]int64 {
	t.Helper()
	all, err := e.tpl.ListInstances(dbtest.Ctx(t), org1)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, inst := range all {
		out[inst.Key] = inst.Revision
	}
	return out
}

func TestQueueUpgradeChecksTheTemplateAndTheScope(t *testing.T) {
	e := newEnv(t)
	_, tu := e.withAgent(t)
	ctx := dbtest.Ctx(t)
	tpl, _ := e.shop(t, tu)
	if _, err := e.uc.QueueUpgrade(ctx, "u1", org2, tpl.ID, 0); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("another org's template: %v", err)
	}
	if _, err := e.uc.QueueUpgrade(ctx, "u1", org1, tpl.ID, 7); !errors.Is(err, model.ErrTemplateRevisionGone) {
		t.Fatalf("an unknown revision: %v", err)
	}
	v, err := e.uc.QueueUpgrade(ctx, "u1", org1, tpl.ID, 0)
	if err != nil || v.State != "queued" || v.Kind != model.RolloutTemplateUpgrade || v.ScopeKey != tpl.ID || v.Revision != 1 || v.CreatedBy != "u1" {
		t.Fatalf("queued = %+v, %v", v, err)
	}
	if stored := e.get(t, v.ID); stored.ScopeKey != model.UpgradeScopeKey(org1, tpl.ID) {
		t.Fatalf("stored scope key = %q", stored.ScopeKey)
	}
	if _, err := e.uc.QueueUpgrade(ctx, "u1", org1, tpl.ID, 0); !errors.Is(err, model.ErrRolloutActive) || !errors.Is(err, model.ErrConflict) {
		t.Fatalf("a second upgrade: %v", err)
	}
	if list, err := e.uc.List(ctx, org1, model.RolloutTemplateUpgrade, tpl.ID); err != nil || len(list) != 1 || list[0].ID != v.ID {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestUpgradeAllMovesEveryInstanceAndSkipsWhatNeedsValues(t *testing.T) {
	e := newEnv(t)
	a, tu := e.withAgent(t)
	ctx := dbtest.Ctx(t)
	tpl, apps := e.shop(t, tu, "acme", "bolt", "coda")
	// r2 refuses "bolt": that instance needs a new value, so it is skipped.
	revise(t, tu, tpl, "^[ac][a-z]+$")
	v, err := e.uc.QueueUpgrade(ctx, "u1", org1, tpl.ID, 0)
	if err != nil || v.Revision != 2 {
		t.Fatalf("queue = %+v, %v", v, err)
	}
	e.uc.pass(ctx)

	r := e.get(t, v.ID)
	if r.State != model.RolloutDone || r.Done != 2 || r.Skipped != 1 || r.Total != 3 || len(r.SkipIDs) != 1 {
		t.Fatalf("rollout = %+v", r)
	}
	if got := revisions(t, e); got["acme"] != 2 || got["bolt"] != 1 || got["coda"] != 2 {
		t.Fatalf("revisions = %v", got)
	}
	// The three creates, then the upgrades oldest first; the skipped
	// instance was not saved again.
	save := func(n string) string { return "app.save " + apps[n] }
	if got, want := a.commands(), []string{save("acme"), save("bolt"), save("coda"), save("acme"), save("coda")}; !slices.Equal(got, want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	evs := e.pub.events()
	if last := evs[len(evs)-1]; last.State != "done" || last.Revision != 2 || last.Done != 2 || last.Skipped != 1 {
		t.Fatalf("last event = %+v", last)
	}
	if !slices.ContainsFunc(evs, func(v model.RolloutView) bool {
		return v.CurrentAppID == apps["coda"] && v.CurrentAppName == "shop-coda"
	}) {
		t.Fatalf("no event names the app in flight: %+v", evs)
	}
}

func TestUpgradeAllStopsAtTheFirstFailure(t *testing.T) {
	e := newEnv(t)
	a, tu := e.withAgent(t)
	ctx := dbtest.Ctx(t)
	tpl, apps := e.shop(t, tu, "acme", "bolt", "coda")
	revise(t, tu, tpl, "^[a-z]+$")
	a.mu.Lock()
	a.fail[apps["bolt"]] = "pull access denied for example/shop"
	a.mu.Unlock()
	v, err := e.uc.QueueUpgrade(ctx, "u1", org1, tpl.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	e.uc.pass(ctx)

	r := e.get(t, v.ID)
	if r.State != model.RolloutFailed || r.Error != "pull access denied for example/shop" || r.CurrentAppID != apps["bolt"] || r.Done != 1 {
		t.Fatalf("rollout = %+v", r)
	}
	if got := revisions(t, e); got["acme"] != 2 || got["bolt"] != 1 || got["coda"] != 1 {
		t.Fatalf("revisions = %v: nothing may follow the failure", got)
	}
}

func TestUpgradeFailsWhenTheAppDoesNotRunAfterwards(t *testing.T) {
	e := newEnv(t)
	a, tu := e.withAgent(t)
	ctx := dbtest.Ctx(t)
	tpl, apps := e.shop(t, tu, "acme")
	revise(t, tu, tpl, "^[a-z]+$")
	a.mu.Lock()
	a.after[apps["acme"]] = command.ContainerStatusRestarting
	a.mu.Unlock()
	v, err := e.uc.QueueUpgrade(ctx, "u1", org1, tpl.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.uc.pass(ctx)
	if r := e.get(t, v.ID); r.State != model.RolloutFailed || r.Error != "container web is restarting" || r.CurrentAppID != apps["acme"] {
		t.Fatalf("rollout = %+v", r)
	}
	// The save itself went through: the instance is on r2.
	if got := revisions(t, e); got["acme"] != 2 {
		t.Fatalf("revisions = %v", got)
	}
}

// markFails fails MarkSaved once on is set: the agent confirmed the save
// but recording it did not happen.
type markFails struct {
	port.TemplateRepository
	on atomic.Bool
}

func (m *markFails) MarkSaved(ctx context.Context, id string, revision int64, params map[string]string, secretNames []string, hosts []model.InstanceHost) error {
	if m.on.Load() {
		return errors.New("database is locked")
	}
	return m.TemplateRepository.MarkSaved(ctx, id, revision, params, secretNames, hosts)
}

// A step that went through but left the instance below the target (its
// save was not recorded) counts as done and is not picked again.
func TestUpgradeDoesNotRepickAnInstanceItCouldNotRecord(t *testing.T) {
	e := newEnv(t)
	a, _ := e.withAgent(t)
	apps, err := apprepo.New(e.d.Tx, e.d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	mf := &markFails{TemplateRepository: e.tpl}
	tu := templates.New(templates.Deps{Repo: mf, Apps: appuc.NewUseCase(&appuc.Deps{CommandDispatcher: a, AppRepository: apps, Log: dbtest.Log}), Log: dbtest.Log})
	e.deps.Instances = tu
	e.uc = e.newUseCase()
	ctx := dbtest.Ctx(t)
	tpl, apps2 := e.shop(t, tu, "acme")
	revise(t, tu, tpl, "^[a-z]+$")
	mf.on.Store(true)
	v, err := e.uc.QueueUpgrade(ctx, "u1", org1, tpl.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.uc.pass(ctx)
	r := e.get(t, v.ID)
	if r.State != model.RolloutDone || r.Done != 1 || r.Skipped != 0 || len(r.SkipIDs) != 1 {
		t.Fatalf("rollout = %+v", r)
	}
	if got := revisions(t, e); got["acme"] != 1 {
		t.Fatalf("revisions = %v", got)
	}
	save := "app.save " + apps2["acme"]
	if got := a.commands(); !slices.Equal(got, []string{save, save}) {
		t.Fatalf("commands = %v: the instance was saved again", got)
	}
}

// Two orgs upgrade the same catalog template at once: each rollout is the
// org's own (scope key <org_id>:<template_id>) and moves only its
// instances.
func TestUpgradeOfACatalogTemplateIsPerOrg(t *testing.T) {
	e := newEnv(t)
	_, tu := e.withAgent(t)
	ctx := dbtest.Ctx(t)
	raw, err := template.Encode(shopBody("^[a-z]+$"))
	if err != nil {
		t.Fatal(err)
	}
	cat := dbtest.ID(41)
	if err := e.tpl.CreateTemplate(ctx, model.Template{ID: cat, Source: model.TemplateSourceCatalog, Slug: "shop", Name: "Shop"},
		model.TemplateRevision{Body: raw}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ org, srv string }{{org1, srv1}, {org2, srv2}} {
		out, err := tu.CreateInstance(ctx, "u1", c.org, templates.CreateInput{ServerID: c.srv, Key: "k", TemplateID: cat, Values: map[string]string{"NAME": "acme"}})
		if err != nil {
			t.Fatal(err)
		}
		<-out.Done
	}
	views := map[string]model.RolloutView{}
	for _, org := range []string{org1, org2} {
		v, err := e.uc.QueueUpgrade(ctx, "u1", org, cat, 0)
		if err != nil || v.ScopeKey != cat {
			t.Fatalf("%s queues = %+v, %v", org, v, err)
		}
		views[org] = v
	}
	if _, err := e.uc.QueueUpgrade(ctx, "u1", org1, cat, 0); !errors.Is(err, model.ErrRolloutActive) {
		t.Fatalf("org1's second upgrade: %v", err)
	}
	for _, org := range []string{org1, org2} {
		list, err := e.uc.List(ctx, org, model.RolloutTemplateUpgrade, cat)
		if err != nil || len(list) != 1 || list[0].ID != views[org].ID {
			t.Fatalf("%s list = %+v, %v", org, list, err)
		}
	}
}

// Deleting a template deletes its queued upgrade rollouts: they are stored
// under the key the repository deletes by.
func TestDeletingATemplateRemovesItsQueuedUpgrade(t *testing.T) {
	e := newEnv(t)
	_, tu := e.withAgent(t)
	ctx := dbtest.Ctx(t)
	tpl, _ := e.shop(t, tu)
	v, err := e.uc.QueueUpgrade(ctx, "u1", org1, tpl.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.tpl.DeleteTemplate(ctx, org1, tpl.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := e.repo.ListRollouts(ctx, org1, model.RolloutTemplateUpgrade, ""); err != nil || len(got) != 0 {
		t.Fatalf("rollouts after the delete = %+v, %v (queued %s)", got, err, v.ID)
	}
}
