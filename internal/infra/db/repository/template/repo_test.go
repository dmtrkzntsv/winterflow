package template

import (
	"errors"
	"sync"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	rolloutrepo "winterflow/internal/infra/db/repository/rollout"
)

var (
	org1, org2 = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2 = dbtest.ID(11), dbtest.ID(12)
	tpl1, tpl2 = dbtest.ID(21), dbtest.ID(22)
)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func mustCreate(t *testing.T, r *Repo, orgID, id, name string) {
	t.Helper()
	err := r.CreateTemplate(dbtest.Ctx(t), model.Template{ID: id, OrgID: orgID, Name: name, CreatedBy: "u"},
		model.TemplateRevision{Body: []byte(`{"files":{}}`), Note: "first", CreatedBy: "u"})
	if err != nil {
		t.Fatal(err)
	}
}

func instance(n int, key string) model.Instance {
	return model.Instance{
		ID: dbtest.ID(100 + n), OrgID: org1, Key: key, AppID: dbtest.ID(200 + n), ServerID: srv1,
		TemplateID: tpl1, Revision: 1, Params: map[string]string{"NAME": "acme"},
		SecretNames: []string{"B", "A"}, RequestHash: "h1",
	}
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestTemplateNamesAreUniquePerOrg(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	err := r.CreateTemplate(ctx, model.Template{ID: tpl2, OrgID: org1, Name: "shop"}, model.TemplateRevision{Body: []byte(`{}`)})
	if !errors.Is(err, model.ErrTemplateNameTaken) || !errors.Is(err, model.ErrRejected) {
		t.Fatalf("same name in the org: %v", err)
	}
	// Another org may use the name.
	mustCreate(t, r, org2, tpl2, "shop")
	list, err := r.ListTemplates(ctx, org1)
	if err != nil || len(list) != 1 || list[0].Name != "shop" || list[0].LatestRevision != 1 {
		t.Fatalf("org1 list = %+v, %v", list, err)
	}
	if _, err := r.GetTemplate(ctx, org2, tpl1); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("another org's template: %v", err)
	}
}

func TestListTemplateNamesIsScopedToTheOrg(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	mustCreate(t, r, org2, tpl2, "blog")
	names, err := r.ListTemplateNames(ctx, org1)
	if err != nil || len(names) != 1 || names[tpl1] != "shop" {
		t.Fatalf("org1 names = %v, %v", names, err)
	}
}

func TestAddRevisionIsOptimistic(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	mustCreate(t, r, org1, tpl2, "blog")
	rev, err := r.AddRevision(ctx, org1, tpl1, "shop-2", 1, model.TemplateRevision{Body: []byte(`{"v":2}`), Note: "two"})
	if err != nil || rev != 2 {
		t.Fatalf("add r2 = %d, %v", rev, err)
	}
	if _, err := r.AddRevision(ctx, org1, tpl1, "shop-2", 1, model.TemplateRevision{Body: []byte(`{}`)}); !errors.Is(err, model.ErrRevisionConflict) {
		t.Fatalf("stale base: %v", err)
	}
	if _, err := r.AddRevision(ctx, org1, tpl1, "blog", 2, model.TemplateRevision{Body: []byte(`{}`)}); !errors.Is(err, model.ErrTemplateNameTaken) {
		t.Fatalf("rename onto another template: %v", err)
	}
	if _, err := r.AddRevision(ctx, org2, tpl1, "x", 2, model.TemplateRevision{Body: []byte(`{}`)}); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("another org: %v", err)
	}
	got, err := r.GetRevision(ctx, tpl1, 2)
	if err != nil || string(got.Body) != `{"v":2}` || got.Note != "two" {
		t.Fatalf("r2 = %+v, %v", got, err)
	}
	if _, err := r.GetRevision(ctx, tpl1, 3); !errors.Is(err, model.ErrTemplateRevisionGone) {
		t.Fatalf("missing revision: %v", err)
	}
	revs, err := r.ListRevisions(ctx, tpl1)
	if err != nil || len(revs) != 2 || revs[0].Revision != 2 || revs[1].Note != "first" || revs[0].Body != nil {
		t.Fatalf("revisions = %+v, %v", revs, err)
	}
	tpl, err := r.GetTemplate(ctx, org1, tpl1)
	if err != nil || tpl.Name != "shop-2" || tpl.LatestRevision != 2 {
		t.Fatalf("template = %+v, %v", tpl, err)
	}
}

func TestClaimInstanceOncePerLiveKey(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	got, inserted, err := r.ClaimInstance(ctx, instance(1, "k1"))
	if err != nil || !inserted || got.State != model.InstancePending || got.Params["NAME"] != "acme" ||
		len(got.SecretNames) != 2 || got.SecretNames[0] != "A" {
		t.Fatalf("first claim = %+v, %v, %v", got, inserted, err)
	}
	again, inserted, err := r.ClaimInstance(ctx, instance(2, "k1"))
	if err != nil || inserted || again.ID != got.ID || again.AppID != got.AppID {
		t.Fatalf("second claim = %+v, %v, %v", again, inserted, err)
	}
	// Another org's key with the same value is its own.
	other := instance(3, "k1")
	other.OrgID, other.ServerID, other.TemplateID = org2, srv2, ""
	if _, inserted, err := r.ClaimInstance(ctx, other); err != nil || !inserted {
		t.Fatalf("other org = %v, %v", inserted, err)
	}
}

// Two creates racing on one key make one instance (and so one app id).
func TestClaimInstanceConcurrent(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	var wg sync.WaitGroup
	apps := make([]string, 8)
	errs := make([]error, 8)
	for i := range apps {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, _, err := r.ClaimInstance(ctx, instance(10+i, "race"))
			apps[i], errs[i] = got.AppID, err
		}(i)
	}
	wg.Wait()
	for i := range apps {
		if errs[i] != nil {
			t.Fatalf("claim %d: %v", i, errs[i])
		}
		if apps[i] != apps[0] {
			t.Fatalf("claims returned different apps: %v", apps)
		}
	}
}

func TestUnlinkFreesTheKeyAndDeleteRetiresIt(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	first, _, _ := r.ClaimInstance(ctx, instance(1, "k"))
	if err := r.MarkSaved(ctx, first.ID, 2, map[string]string{"NAME": "b"}, nil); err != nil {
		t.Fatal(err)
	}
	saved, err := r.GetInstance(ctx, org1, "k")
	if err != nil || saved.State != model.InstanceCreated || saved.Revision != 2 || saved.Params["NAME"] != "b" || len(saved.SecretNames) != 0 {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	if err := r.SetState(ctx, first.ID, model.InstanceUnlinked); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetInstance(ctx, org1, "k"); !errors.Is(err, model.ErrInstanceNotFound) {
		t.Fatalf("unlinked key: %v", err)
	}
	second, inserted, err := r.ClaimInstance(ctx, instance(2, "k"))
	if err != nil || !inserted || second.ID == first.ID {
		t.Fatalf("reclaim after unlink = %+v, %v, %v", second, inserted, err)
	}
	if err := r.SetState(ctx, second.ID, model.InstanceDeleted); err != nil {
		t.Fatal(err)
	}
	if _, inserted, _ := r.ClaimInstance(ctx, instance(3, "k")); inserted {
		t.Fatal("a deleted instance's key was claimed again")
	}
	// A late save confirmation does not revive a deleted instance.
	if err := r.MarkSaved(ctx, second.ID, 3, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.GetInstance(ctx, org1, "k"); got.State != model.InstanceDeleted {
		t.Fatalf("after a late save: %+v", got)
	}
	if err := r.SetError(ctx, second.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.GetInstance(ctx, org1, "k"); got.LastError != "boom" {
		t.Fatalf("last error = %q", got.LastError)
	}
}

func TestDeleteTemplateRefusedWhileLive(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	live, _, _ := r.ClaimInstance(ctx, instance(1, "live"))
	gone, _, _ := r.ClaimInstance(ctx, instance(2, "gone"))
	if err := r.SetState(ctx, gone.ID, model.InstanceDeleted); err != nil {
		t.Fatal(err)
	}
	list, _ := r.ListTemplates(ctx, org1)
	if len(list) != 1 || list[0].Instances != 1 || list[0].Behind != 0 {
		t.Fatalf("counts = %+v", list)
	}
	if err := r.DeleteTemplate(ctx, org1, tpl1); !errors.Is(err, model.ErrTemplateInUse) {
		t.Fatalf("delete with a live instance: %v", err)
	}
	if err := r.DeleteTemplate(ctx, org2, tpl1); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("delete from another org: %v", err)
	}
	if err := r.SetState(ctx, live.ID, model.InstanceUnlinked); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteTemplate(ctx, org1, tpl1); err != nil {
		t.Fatal(err)
	}
	if n := dbtest.QueryString(t, d, `SELECT COUNT(*) FROM template_revisions WHERE template_id = ?`, tpl1); n != "0" {
		t.Fatalf("revisions left: %s", n)
	}
	// The retired rows outlive the template with template_id NULL.
	if got, err := r.GetInstance(ctx, org1, "gone"); err != nil || got.TemplateID != "" {
		t.Fatalf("retired row = %+v, %v", got, err)
	}
	if n := dbtest.QueryString(t, d, `SELECT COUNT(*) FROM template_instances WHERE template_id IS NULL`); n != "2" {
		t.Fatalf("rows with template_id NULL: %s", n)
	}
}

func TestListInstancesAndBehind(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	a, _, _ := r.ClaimInstance(ctx, instance(1, "a"))
	_, _, _ = r.ClaimInstance(ctx, instance(2, "b"))
	if _, err := r.AddRevision(ctx, org1, tpl1, "shop", 1, model.TemplateRevision{Body: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkSaved(ctx, a.ID, 2, nil, nil); err != nil {
		t.Fatal(err)
	}
	list, _ := r.ListTemplates(ctx, org1)
	if list[0].Instances != 2 || list[0].Behind != 1 {
		t.Fatalf("counts = %+v", list[0])
	}
	got, err := r.ListInstances(ctx, org1)
	if err != nil || len(got) != 2 || got[0].Key != "a" || got[1].Key != "b" {
		t.Fatalf("list = %+v, %v", got, err)
	}
	if other, _ := r.ListInstances(ctx, org2); len(other) != 0 {
		t.Fatalf("org2 sees %+v", other)
	}
}

// A rollout of an org touches only instances on the org's own servers.
func TestListInstancesOnOrgServers(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	_, _, _ = r.ClaimInstance(ctx, instance(1, "own"))
	stray := instance(2, "stray")
	stray.ServerID = srv2 // org2's server
	_, _, _ = r.ClaimInstance(ctx, stray)
	if all, _ := r.ListInstances(ctx, org1); len(all) != 2 {
		t.Fatalf("all = %+v", all)
	}
	got, err := r.ListInstancesOnOrgServers(ctx, org1)
	if err != nil || len(got) != 1 || got[0].Key != "own" {
		t.Fatalf("on org1's servers = %+v, %v", got, err)
	}
}

// The app routes see an app that is a live instance; delete-app retires
// its key. An unlinked instance is no longer the app's, yet still read by
// id.
func TestInstanceLinksForApp(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	inst := instance(1, "k1")
	if _, _, err := r.ClaimInstance(ctx, inst); err != nil {
		t.Fatal(err)
	}
	if name, ok, err := r.LiveInstanceForApp(ctx, srv1, inst.AppID); err != nil || !ok || name != "shop" {
		t.Fatalf("a pending instance's app = %q, %v, %v", name, ok, err)
	}
	if _, ok, err := r.LiveInstanceForApp(ctx, srv2, inst.AppID); err != nil || ok {
		t.Fatalf("the app id on another server = %v, %v", ok, err)
	}
	if _, ok, err := r.LiveInstanceForApp(ctx, srv1, dbtest.ID(999)); err != nil || ok {
		t.Fatalf("an ordinary app = %v, %v", ok, err)
	}
	if err := r.RetireForApp(ctx, srv2, inst.AppID); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.GetInstance(ctx, org1, "k1"); got.State != model.InstancePending {
		t.Fatalf("a delete on another server retired the key: %+v", got)
	}
	if err := r.SetError(ctx, inst.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	if err := r.RetireForApp(ctx, srv1, inst.AppID); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetInstance(ctx, org1, "k1")
	if err != nil || got.State != model.InstanceDeleted || got.LastError != "" {
		t.Fatalf("after delete-app = %+v, %v", got, err)
	}
	if _, ok, _ := r.LiveInstanceForApp(ctx, srv1, inst.AppID); ok {
		t.Fatal("a deleted instance still holds its app")
	}

	other := instance(2, "k2")
	if _, _, err := r.ClaimInstance(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := r.SetState(ctx, other.ID, model.InstanceUnlinked); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := r.LiveInstanceForApp(ctx, srv1, other.AppID); ok {
		t.Fatal("an unlinked instance still holds its app")
	}
	if err := r.RetireForApp(ctx, srv1, other.AppID); err != nil {
		t.Fatal(err)
	}
	byID, err := r.GetInstanceByID(ctx, other.ID)
	if err != nil || byID.State != model.InstanceUnlinked || byID.Key != "k2" {
		t.Fatalf("by id = %+v, %v", byID, err)
	}
	if _, err := r.GetInstanceByID(ctx, dbtest.ID(998)); !errors.Is(err, model.ErrInstanceNotFound) {
		t.Fatalf("an unknown id: %v", err)
	}
}

// Deleting a template deletes its upgrade rollouts (scope key: the template
// id), not another template's nor the org's reloads; a refused delete
// keeps them.
func TestDeleteTemplateDeletesItsRollouts(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	rollouts, err := rolloutrepo.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, r, org1, tpl1, "shop")
	mustCreate(t, r, org1, tpl2, "blog")
	for i, ro := range []model.Rollout{
		{Kind: model.RolloutTemplateUpgrade, ScopeKey: tpl1},
		{Kind: model.RolloutTemplateUpgrade, ScopeKey: tpl2},
		{Kind: model.RolloutVarsReload, ScopeKey: org1},
	} {
		ro.ID, ro.OrgID, ro.Scope = dbtest.ID(300+i), org1, []byte(`{}`)
		if ok, err := rollouts.InsertRollout(ctx, ro); err != nil || !ok {
			t.Fatalf("insert %d: %v %v", i, ok, err)
		}
	}
	count := func() string {
		return dbtest.QueryString(t, d, `SELECT COUNT(*) FROM rollouts`)
	}
	live, _, _ := r.ClaimInstance(ctx, instance(1, "live"))
	if err := r.DeleteTemplate(ctx, org1, tpl1); !errors.Is(err, model.ErrTemplateInUse) || count() != "3" {
		t.Fatalf("a refused delete: %v, %s rollouts left", err, count())
	}
	if err := r.SetState(ctx, live.ID, model.InstanceUnlinked); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteTemplate(ctx, org1, tpl1); err != nil {
		t.Fatal(err)
	}
	if n := dbtest.QueryString(t, d, `SELECT COUNT(*) FROM rollouts WHERE scope_key = ?`, tpl1); n != "0" || count() != "2" {
		t.Fatalf("rollouts of the deleted template: %s, all: %s", n, count())
	}
}
