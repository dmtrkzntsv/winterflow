package templates_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/template"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/pkg/crypto/cryptotest"
)

// create is a create-instance of key k1 on srv1 with the given values.
func (f fixture) create(values map[string]string) templates.CreateInput {
	return templates.CreateInput{ServerID: srv1, Key: "k1", TemplateID: f.tpl.ID, Values: values, Secrets: map[string]string{"KEY": f.secret}}
}

// received waits for an outcome's result.
func received(t *testing.T, done <-chan port.CommandResult) port.CommandResult {
	t.Helper()
	select {
	case res := <-done:
		return res
	case <-time.After(5 * time.Second):
		t.Fatal("no result delivered")
	}
	return port.CommandResult{}
}

func TestCreateClaimsDispatchesAndRecords(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	out, err := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "acme"}))
	if err != nil || out.RequestID == "" || out.Instance != nil {
		t.Fatalf("create = %+v, %v", out, err)
	}
	pending, err := f.uc.GetInstance(ctx, org1, srv1, "k1")
	if err != nil || pending.State != "pending" || pending.AppID == "" {
		t.Fatalf("before the result = %+v, %v", pending, err)
	}
	req := f.disp.last(t).Payload.(command.SaveAppRequest)
	if req.App.AppID != pending.AppID || f.disp.last(t).AgentID != srv1 {
		t.Fatalf("dispatched %+v for %s", req.App.AppID, f.disp.last(t).AgentID)
	}
	// The result must outlive the wait bound, or a late one is dropped and
	// the instance stays pending.
	if f.disp.last(t).Wait != appuc.ObservedResultTTL {
		t.Fatalf("dispatched with wait %v, want %v", f.disp.last(t).Wait, appuc.ObservedResultTTL)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	if res := received(t, out.Done); !res.Success {
		t.Fatalf("result = %+v", res)
	}
	got, err := out.Final(ctx)
	if err != nil || got.State != "created" || got.Values["NAME"] != "acme" || got.Secrets["KEY"] != command.EncryptedPlaceholder ||
		got.Name != "shop" || len(got.Hosts) != 0 {
		t.Fatalf("after the result = %+v, %v", got, err)
	}
	list, err := f.uc.ListInstances(ctx, org1, srv1, f.tpl.ID)
	if err != nil || len(list.Instances) != 1 || list.Instances[0].TemplateName != "shop" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if other, _ := f.uc.ListInstances(ctx, org1, srv2, ""); len(other.Instances) != 0 {
		t.Fatalf("another server's list = %+v", other)
	}
	if _, err := f.uc.ListInstances(ctx, org2, "", f.tpl.ID); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("another org's template filter: %v", err)
	}
}

// A retried create carries a new ECIES ciphertext for the same secret: it
// is the same request (secrets compare by name).
func TestCreateReplays(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	in := f.create(map[string]string{"NAME": "acme"})
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, in); err != nil {
		t.Fatal(err)
	}
	appID := f.disp.last(t).Payload.(command.SaveAppRequest).App.AppID

	// Pending (never confirmed): the same request dispatches the save again,
	// with the same app id.
	in.Secrets = map[string]string{"KEY": cryptotest.Ciphertext(t, "key")}
	again, err := f.uc.CreateInstance(ctx, "u1", org1, in)
	if err != nil || again.RequestID == "" || f.disp.count() != 2 ||
		f.disp.last(t).Payload.(command.SaveAppRequest).App.AppID != appID {
		t.Fatalf("pending replay = %+v, %v", again, err)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, again.Done)

	// Created: answered from the DB, nothing dispatched.
	done, err := f.uc.CreateInstance(ctx, "u1", org1, in)
	if err != nil || done.Instance == nil || done.Instance.AppID != appID || f.disp.count() != 2 {
		t.Fatalf("created replay = %+v, %v", done, err)
	}
	// Different values: a conflict.
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "other"})); !errors.Is(err, model.ErrKeyInUse) || !errors.Is(err, model.ErrConflict) {
		t.Fatalf("different values: %v", err)
	}
}

// A create renders the app the caller asked for: its name, a hostname per
// http expose entry, the backups it turned on, one variable per value.
func TestCreateRendersNameHostsAndBackups(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	in := f.create(map[string]string{"NAME": "acme", "VERSION": ""})
	in.Name = "Acme shop"
	in.Hosts = []model.InstanceHost{{Host: "acme.example.test", Expose: "app:8080"}}
	in.Backups = map[string]template.BackupChoice{"db": {Enabled: true, Storage: "disk"}}
	out, err := f.uc.CreateInstance(ctx, "u1", org1, in)
	if err != nil {
		t.Fatal(err)
	}
	vars, cfg := saved(t, f)
	if len(vars) != 2 || string(vars["NAME"].Content) != "acme" || string(vars["KEY"].Content) != f.secret || !vars["KEY"].Encrypted {
		t.Fatalf("variables = %+v", vars)
	}
	if cfg.Name != "Acme shop" || len(cfg.Ingress.Domains) != 1 || cfg.Ingress.Domains[0].Expose != "app:8080" ||
		len(cfg.Backups) != 1 || cfg.Backups[0].Storage != "disk" || cfg.Instance.Key != "k1" {
		t.Fatalf("config = %+v", cfg)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, out.Done)
	if v, _ := out.Final(ctx); v.Name != "Acme shop" || len(v.Hosts) != 1 || v.Hosts[0].Host != "acme.example.test" {
		t.Fatalf("created = %+v", v)
	}
}

// A create whose rendered app fails a save-app check claims nothing: the
// caller can fix the request and use the same key.
func TestCreateRefusedBeforeTheClaim(t *testing.T) {
	f := newFixture(t, noStorages{})
	ctx := dbtest.Ctx(t)
	in := f.create(map[string]string{"NAME": "acme"})
	in.Backups = map[string]template.BackupChoice{"db": {Enabled: true, Storage: "nowhere"}}
	_, err := f.uc.CreateInstance(ctx, "u1", org1, in)
	if !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), `no storage "nowhere"`) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.uc.GetInstance(ctx, org1, srv1, "k1"); !errors.Is(err, model.ErrInstanceNotFound) || f.disp.count() != 0 {
		t.Fatalf("a refused create claimed the key: %v", err)
	}
	in.Backups = nil // the backup starts off
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, in); err != nil {
		t.Fatalf("the fixed request: %v", err)
	}
}

func TestCreateErrors(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	if _, err := f.uc.CreateInstance(ctx, "u1", org2, f.create(map[string]string{"NAME": "acme"})); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("another org's template: %v", err)
	}
	bad := f.create(map[string]string{"NAME": "acme"})
	bad.Key = "has space"
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, bad); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("bad key: %v", err)
	}
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "UPPER"})); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("off-pattern value: %v", err)
	}
	named := f.create(map[string]string{"NAME": "acme"})
	named.Name = " padded"
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, named); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "name") {
		t.Fatalf("a bad app name: %v", err)
	}
	hosted := f.create(map[string]string{"NAME": "acme"})
	hosted.Hosts = []model.InstanceHost{{Host: "a.example.test", Expose: "app:9999"}}
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, hosted); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "hosts[0].expose") {
		t.Fatalf("a host on no http entry: %v", err)
	}
	missing := f.create(map[string]string{"NAME": "acme"})
	missing.Revision = 7
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, missing); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing revision: %v", err)
	}
	backups := f.create(map[string]string{"NAME": "acme"})
	backups.Backups = map[string]template.BackupChoice{"media": {Storage: "disk"}}
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, backups); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("a setting of an unknown backup: %v", err)
	}
	out, _ := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "acme"}))
	f.disp.reply(t, port.CommandResult{Error: "pull failed"})
	if res := received(t, out.Done); res.Success {
		t.Fatal("a failure was delivered as a success")
	}
	if got, _ := f.uc.GetInstance(ctx, org1, srv1, "k1"); got.State != "pending" || got.LastError != "pull failed" {
		t.Fatalf("after a failed create = %+v", got)
	}
	if _, err := f.uc.GetInstance(ctx, org1, srv2, "k1"); !errors.Is(err, model.ErrInstanceNotFound) {
		t.Fatalf("on another server: %v", err)
	}
}

// A caller that stopped waiting (its client went away, or the wait bound
// passed) must not lose the outcome: the result is recorded anyway and
// delivering it never blocks the bus.
func TestResultRecordedWithoutAWaiter(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "acme"})); err != nil {
		t.Fatal(err)
	}
	in := f.disp.last(t)
	finished := make(chan struct{})
	go func() {
		in.OnResult(port.CommandResult{Success: true})
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the result hook blocked on a caller that is not listening")
	}
	if got, _ := f.uc.GetInstance(ctx, org1, srv1, "k1"); got.State != "created" {
		t.Fatalf("after an unwatched result = %+v", got)
	}
}

// statuses is a status cache with fixed statuses; reads counts its reads
// per server.
type statuses struct {
	by    map[string][]command.AppStatus
	reads map[string]int
}

func (s *statuses) AppStatuses(serverID string, _ time.Time) []command.AppStatus {
	s.reads[serverID]++
	return s.by[serverID]
}

// A view carries its app's status; a list reads each server's statuses once.
func TestViewsCarryTheAppStatus(t *testing.T) {
	st := &statuses{by: map[string][]command.AppStatus{}, reads: map[string]int{}}
	f := newFixtureWith(t, nil, st)
	ctx := dbtest.Ctx(t)
	for _, key := range []string{"k1", "k2"} {
		in := f.create(map[string]string{"NAME": "acme"})
		in.Key = key
		if _, err := f.uc.CreateInstance(ctx, "u1", org1, in); err != nil {
			t.Fatal(err)
		}
	}
	k1, err := f.uc.GetInstance(ctx, org1, srv1, "k1")
	if err != nil || k1.StatusCode != command.ContainerStatusUnknown {
		t.Fatalf("k1 without a status = %+v, %v", k1, err)
	}
	st.by[srv1] = []command.AppStatus{{AppID: k1.AppID, StatusCode: command.ContainerStatusActive}}
	if got, _ := f.uc.GetInstance(ctx, org1, srv1, "k1"); got.StatusCode != command.ContainerStatusActive {
		t.Fatalf("k1 status = %v", got.StatusCode)
	}
	st.reads[srv1] = 0
	list, err := f.uc.ListInstances(ctx, org1, "", "")
	if err != nil || len(list.Instances) != 2 || st.reads[srv1] != 1 {
		t.Fatalf("list = %+v, %v (%d status reads)", list, err, st.reads[srv1])
	}
	for _, v := range list.Instances {
		want := command.ContainerStatusUnknown
		if v.Key == "k1" {
			want = command.ContainerStatusActive
		}
		if v.StatusCode != want {
			t.Errorf("%s status = %v, want %v", v.Key, v.StatusCode, want)
		}
	}
}

// A catalog template is installed like an org's own; one the catalog
// withdrew takes no new installs.
func TestCreateFromTheCatalog(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	raw, err := template.Encode(body())
	if err != nil {
		t.Fatal(err)
	}
	catID, goneID := dbtest.ID(41), dbtest.ID(42)
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []model.Template{
		{ID: catID, Source: model.TemplateSourceCatalog, Slug: "shop", Name: "Shop"},
		{ID: goneID, Source: model.TemplateSourceCatalog, Slug: "gone", Name: "Gone", WithdrawnAt: &at},
	} {
		if err := f.repo.CreateTemplate(ctx, c, model.TemplateRevision{Body: raw, Digest: "sha256:x"}); err != nil {
			t.Fatal(err)
		}
	}
	in := f.create(map[string]string{"NAME": "acme"})
	in.TemplateID = catID
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, in); err != nil {
		t.Fatal(err)
	}
	if _, cfg := saved(t, f); cfg.Name != "Shop" || cfg.Instance.TemplateID != catID {
		t.Fatalf("config = %+v", cfg)
	}
	in.Key, in.TemplateID = "k2", goneID
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, in); !errors.Is(err, model.ErrTemplateWithdrawn) || !errors.Is(err, model.ErrConflict) {
		t.Fatalf("a withdrawn template: %v", err)
	}
	list, err := f.uc.ListInstances(ctx, org2, "", catID)
	if err != nil || len(list.Instances) != 0 {
		t.Fatalf("another org's instances of the catalog template = %+v, %v", list, err)
	}
}
