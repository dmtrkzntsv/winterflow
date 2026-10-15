package templates_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/template"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/pkg/crypto/cryptotest"
)

// created is fixture f with instance k1 created (NAME=acme).
func created(t *testing.T, f fixture) {
	t.Helper()
	out, err := f.uc.CreateInstance(dbtest.Ctx(t), "u1", org1, f.create(map[string]string{"NAME": "acme"}))
	if err != nil {
		t.Fatal(err)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, out.Done)
}

// pendingInstance is fixture f with instance k1 pending: the save was
// dispatched and never confirmed.
func pendingInstance(t *testing.T, f fixture) {
	t.Helper()
	if _, err := f.uc.CreateInstance(dbtest.Ctx(t), "u1", org1, f.create(map[string]string{"NAME": "acme"})); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateKeepsValuesAndSecrets(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	created(t, f)
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "nope"}); !errors.Is(err, model.ErrInstanceNotFound) {
		t.Fatalf("unknown key: %v", err)
	}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv2, Key: "k1"}); !errors.Is(err, model.ErrInstanceNotFound) {
		t.Fatalf("on another server: %v", err)
	}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Values: map[string]string{"NOPE": "x"}}); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("an undeclared variable: %v", err)
	}
	out, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Values: map[string]string{"VERSION": "2"}})
	if err != nil {
		t.Fatal(err)
	}
	req := f.disp.last(t).Payload.(command.SaveAppRequest)
	for _, v := range req.App.Variables {
		if v.Name == "KEY" && (string(v.Content) != command.EncryptedPlaceholder || !v.Encrypted) {
			t.Fatalf("an update re-sent the secret: %+v", v)
		}
	}
	if strings.Contains(string(req.App.Config), `"backups"`) {
		t.Fatalf("an update replaced the backups list: %s", req.App.Config)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, out.Done)
	if v, _ := out.Final(ctx); v.Values["VERSION"] != "2" || v.Values["NAME"] != "acme" || v.Revision != 1 {
		t.Fatalf("after update = %+v", v)
	}
}

// An update renders the app under its current name (a rename since the
// create counts) and keeps the hostnames unless new ones are given.
func TestUpdateKeepsTheNameAndHosts(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	in := f.create(map[string]string{"NAME": "acme"})
	in.Name = "Acme"
	in.Hosts = []model.InstanceHost{{Host: "acme.example.test", Expose: "app:8080"}}
	out, err := f.uc.CreateInstance(ctx, "u1", org1, in)
	if err != nil {
		t.Fatal(err)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, out.Done)
	appID := f.disp.last(t).Payload.(command.SaveAppRequest).App.AppID
	if err := f.apps.RenameApp(ctx, appID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	if _, cfg := saved(t, f); cfg.Name != "Renamed" || len(cfg.Ingress.Domains) != 1 || cfg.Ingress.Domains[0].Domain != "acme.example.test" {
		t.Fatalf("an update's config = %+v", cfg)
	}
	none := []model.InstanceHost{}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Hosts: &none}); err != nil {
		t.Fatal(err)
	}
	if _, cfg := saved(t, f); len(cfg.Ingress.Domains) != 0 {
		t.Fatalf("hosts not replaced: %+v", cfg.Ingress)
	}
	bad := []model.InstanceHost{{Host: "x.example.test", Expose: "db:1"}}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Hosts: &bad}); !errors.Is(err, model.ErrRejected) || errors.Is(err, templates.ErrNeedsValues) {
		t.Fatalf("a given host on no http entry: %v", err)
	}
}

// An upgrade re-renders with the stored values: a newly required variable
// without one is ErrNeedsValues (as is a stored hostname whose expose entry
// the revision dropped), and it sends no backups list, so a backup the
// revision adds starts off.
func TestUpgradeNeedsValuesAndSendsNoBackups(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	created(t, f)

	b := body()
	b.Files["compose.yaml"] = strings.NewReplacer(
		"path: shop.db }\n", "path: shop.db }\n    - { name: media, type: files, volume: data }\n",
		"      KEY: \"changeme\"\n", "      KEY: \"changeme\"\n      REGION: \"\"\n",
		"    VERSION: {}\n", "    VERSION: {}\n    REGION: { required: true }\n",
	).Replace(compose)
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: b, BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	_, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 2})
	if !errors.Is(err, model.ErrRejected) || !errors.Is(err, templates.ErrNeedsValues) || !strings.Contains(err.Error(), "variable REGION: a value is required") {
		t.Fatalf("a newly required value missing: %v", err)
	}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 5}); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("an unknown revision: %v", err)
	}
	up, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 2, Values: map[string]string{"REGION": "eu"}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Backups  json.RawMessage `json:"backups"`
		Instance template.Marker `json:"instance"`
	}
	_ = json.Unmarshal(f.disp.last(t).Payload.(command.SaveAppRequest).App.Config, &cfg)
	if cfg.Backups != nil || cfg.Instance.Revision != 2 {
		t.Fatalf("upgrade config: backups %s marker %+v", cfg.Backups, cfg.Instance)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, up.Done)
	if v, _ := up.Final(ctx); v.Revision != 2 || v.Values["REGION"] != "eu" {
		t.Fatalf("upgraded = %+v", v)
	}
	list, err := f.uc.ListTemplates(ctx, org1)
	if err != nil || list.Templates[0].Instances != 1 || list.Templates[0].Behind != 0 {
		t.Fatalf("templates = %+v, %v", list, err)
	}

	// Revision 3 no longer exposes app:8080: the stored hostname can't
	// follow, so Upgrade all would skip the instance.
	hosted := f.create(map[string]string{"NAME": "acme"})
	hosted.Key, hosted.Revision = "k2", 1
	hosted.Hosts = []model.InstanceHost{{Host: "k2.example.test", Expose: "app:8080"}}
	out, err := f.uc.CreateInstance(ctx, "u1", org1, hosted)
	if err != nil {
		t.Fatal(err)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, out.Done)
	b.Files["compose.yaml"] = strings.Replace(b.Files["compose.yaml"], "    - { container: app, port: 8080 }\n", "    - { container: app, port: 9090 }\n", 1)
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: b, BaseRevision: 2}); err != nil {
		t.Fatal(err)
	}
	_, err = f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k2", Revision: 3, Values: map[string]string{"REGION": "eu"}})
	if !errors.Is(err, templates.ErrNeedsValues) || !strings.Contains(err.Error(), "hosts[0].expose") {
		t.Fatalf("a stored host the revision can't route: %v", err)
	}
}

// savedConfig is what the tests read of a dispatched save's config.
type savedConfig struct {
	Name     string
	Ingress  model.Ingress
	Backups  []struct{ Name, Storage string }
	Instance template.Marker
}

// saved is the last dispatched save's variable by name and its config.
func saved(t *testing.T, f fixture) (vars map[string]command.ContentItem, cfg savedConfig) {
	t.Helper()
	req := f.disp.last(t).Payload.(command.SaveAppRequest)
	vars = map[string]command.ContentItem{}
	for _, v := range req.App.Variables {
		vars[v.Name] = v
	}
	if err := json.Unmarshal(req.App.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	return vars, cfg
}

// A pending instance was never confirmed: the agent holds none of its
// secrets and no backups list, so an update sends the secrets again (no
// keep placeholder) and the whole backups list.
func TestUpdatePendingSendsSecretsAndBackups(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	pendingInstance(t, f)

	in := templates.UpdateInput{ServerID: srv1, Key: "k1", Values: map[string]string{"VERSION": "2"}}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, in); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "variable KEY: a value is required") {
		t.Fatalf("a pending update without the required secret: %v", err)
	}
	if f.disp.count() != 1 {
		t.Fatalf("a refused update dispatched: %d commands", f.disp.count())
	}

	again := cryptotest.Ciphertext(t, "key")
	in.Secrets = map[string]string{"KEY": again}
	out, err := f.uc.UpdateInstance(ctx, "u1", org1, in)
	if err != nil {
		t.Fatal(err)
	}
	vars, cfg := saved(t, f)
	if k := vars["KEY"]; string(k.Content) != again || !k.Encrypted {
		t.Fatalf("the secret = %+v", k)
	}
	if cfg.Backups == nil || len(cfg.Backups) != 0 {
		t.Fatalf("config: backups %+v (want the whole, empty list)", cfg.Backups)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, out.Done)
	if v, _ := out.Final(ctx); v.State != "created" || v.Values["VERSION"] != "2" || v.Values["NAME"] != "acme" || v.Secrets["KEY"] != command.EncryptedPlaceholder {
		t.Fatalf("after the update = %+v", v)
	}
}

func TestUpdatePendingBackupsOverride(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	pendingInstance(t, f)

	in := templates.UpdateInput{ServerID: srv1, Key: "k1", Secrets: map[string]string{"KEY": f.secret}, Backups: map[string]template.BackupChoice{"nope": {Storage: "disk"}}}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, in); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("a setting of an unknown backup: %v", err)
	}
	in.Backups = map[string]template.BackupChoice{"db": {Storage: "disk"}}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, in); err != nil {
		t.Fatal(err)
	}
	if _, cfg := saved(t, f); len(cfg.Backups) != 1 || cfg.Backups[0].Storage != "disk" {
		t.Fatalf("backups = %+v", cfg.Backups)
	}
}

func TestUpgradePendingSendsTheWholeBackupsList(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	pendingInstance(t, f)

	b := body()
	b.Files["compose.yaml"] = strings.Replace(compose, "path: shop.db }\n", "path: shop.db }\n    - { name: media, type: files, volume: data }\n", 1)
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: b, BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 2,
		Secrets: map[string]string{"KEY": f.secret}, Backups: map[string]template.BackupChoice{"media": {Storage: "disk"}}}); err != nil {
		t.Fatal(err)
	}
	_, cfg := saved(t, f)
	if len(cfg.Backups) != 1 || cfg.Backups[0].Name != "media" || cfg.Backups[0].Storage != "disk" || cfg.Instance.Revision != 2 {
		t.Fatalf("config: backups %+v marker %+v", cfg.Backups, cfg.Instance)
	}
}

// Backups of a created instance are the app's own: they change in its
// Backups tab, not through an update.
func TestUpdateCreatedRefusesBackups(t *testing.T) {
	f := newFixture(t, nil)
	created(t, f)
	sent := f.disp.count()
	_, err := f.uc.UpdateInstance(dbtest.Ctx(t), "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Backups: map[string]template.BackupChoice{"db": {Storage: "disk"}}})
	if !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "backups can only be set while the instance is pending") {
		t.Fatalf("err = %v", err)
	}
	if f.disp.count() != sent {
		t.Fatal("a refused update dispatched")
	}
}

// A pending instance keeps its create's backup settings: an update that
// sends none renders them again (the web's instance panel never sends
// any); a sent list replaces them; a backup a later revision drops goes.
func TestUpdatePendingKeepsTheCreatesBackups(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	in := f.create(map[string]string{"NAME": "acme"})
	in.Backups = map[string]template.BackupChoice{"db": {Enabled: true, Storage: "disk"}}
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, in); err != nil {
		t.Fatal(err)
	}
	update := templates.UpdateInput{ServerID: srv1, Key: "k1", Secrets: map[string]string{"KEY": f.secret}}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, update); err != nil {
		t.Fatal(err)
	}
	if _, cfg := saved(t, f); len(cfg.Backups) != 1 || cfg.Backups[0].Name != "db" || cfg.Backups[0].Storage != "disk" {
		t.Fatalf("an update without backups: %+v", cfg.Backups)
	}

	sent := update
	sent.Backups = map[string]template.BackupChoice{"db": {Storage: "nas"}}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, sent); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, update); err != nil {
		t.Fatal(err)
	}
	if _, cfg := saved(t, f); len(cfg.Backups) != 1 || cfg.Backups[0].Storage != "nas" {
		t.Fatalf("after a sent list: %+v", cfg.Backups)
	}

	b := body()
	b.Files["compose.yaml"] = strings.Replace(compose, "  backups:\n    - { name: db, type: sqlite, volume: data, path: shop.db }\n", "", 1)
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: b, BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	update.Revision = 2
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, update); err != nil {
		t.Fatalf("an upgrade dropping the backup: %v", err)
	}
	if _, cfg := saved(t, f); cfg.Backups == nil || len(cfg.Backups) != 0 || cfg.Instance.Revision != 2 {
		t.Fatalf("after the upgrade: %+v %+v", cfg.Backups, cfg.Instance)
	}
}
