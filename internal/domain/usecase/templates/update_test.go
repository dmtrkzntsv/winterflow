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
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Params: map[string]string{"NOPE": "x"}}); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("unknown parameter: %v", err)
	}
	out, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Params: map[string]string{"VERSION": "2"}})
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
	if v, _ := out.Final(ctx); v.Parameters["VERSION"] != "2" || v.Parameters["NAME"] != "acme" || v.Revision != 1 {
		t.Fatalf("after update = %+v", v)
	}
}

func TestUpgradeSendsNewBackupDefaults(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	created(t, f)

	b := body()
	b.Files["compose.yaml"] = strings.Replace(compose, "path: shop.db }\n", "path: shop.db }\n    - { name: media, type: files, volume: data }\n", 1)
	b.Backups["media"] = template.BackupDefault{Every: "6h"}
	b.Parameters = append(b.Parameters, template.Parameter{Name: "REGION", Required: true})
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: b, BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	_, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 2})
	if !errors.Is(err, model.ErrRejected) || !errors.Is(err, templates.ErrNeedsValues) || !strings.Contains(err.Error(), "parameters.REGION: required") {
		t.Fatalf("a newly required value missing: %v", err)
	}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 5}); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("an unknown revision: %v", err)
	}
	up, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 2, Params: map[string]string{"REGION": "eu"}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Backups  json.RawMessage `json:"backups"`
		Instance template.Marker `json:"instance"`
	}
	_ = json.Unmarshal(f.disp.last(t).Payload.(command.SaveAppRequest).App.Config, &cfg)
	if cfg.Backups != nil || cfg.Instance.Revision != 2 || len(cfg.Instance.BackupDefaults) != 1 || cfg.Instance.BackupDefaults[0].Name != "media" {
		t.Fatalf("upgrade config: backups %s marker %+v", cfg.Backups, cfg.Instance)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, up.Done)
	if v, _ := up.Final(ctx); v.Revision != 2 || v.Parameters["REGION"] != "eu" {
		t.Fatalf("upgraded = %+v", v)
	}
	list, err := f.uc.ListTemplates(ctx, org1)
	if err != nil || list.Templates[0].Instances != 1 || list.Templates[0].Behind != 0 {
		t.Fatalf("templates = %+v, %v", list, err)
	}
}

// saved is the last dispatched save's variable by name and its config.
func saved(t *testing.T, f fixture) (vars map[string]command.ContentItem, cfg struct {
	Backups  []struct{ Name, Storage string }
	Instance template.Marker
}) {
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

	in := templates.UpdateInput{ServerID: srv1, Key: "k1", Params: map[string]string{"VERSION": "2"}}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, in); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "secrets.KEY") {
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
	if len(cfg.Backups) != 1 || cfg.Backups[0].Name != "db" || len(cfg.Instance.BackupDefaults) != 0 {
		t.Fatalf("config: backups %+v marker %+v", cfg.Backups, cfg.Instance)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, out.Done)
	if v, _ := out.Final(ctx); v.State != "created" || v.Parameters["VERSION"] != "2" || v.Parameters["NAME"] != "acme" || v.Secrets["KEY"] != command.EncryptedPlaceholder {
		t.Fatalf("after the update = %+v", v)
	}
}

func TestUpdatePendingBackupsOverride(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	pendingInstance(t, f)

	in := templates.UpdateInput{ServerID: srv1, Key: "k1", Secrets: map[string]string{"KEY": f.secret}, Backups: map[string]string{"nope": "disk"}}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, in); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("an override of an unknown backup: %v", err)
	}
	in.Backups = map[string]string{"db": "disk"}
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
	b.Backups["media"] = template.BackupDefault{Every: "6h"}
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: b, BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 2,
		Secrets: map[string]string{"KEY": f.secret}, Backups: map[string]string{"media": "disk"}}); err != nil {
		t.Fatal(err)
	}
	_, cfg := saved(t, f)
	if len(cfg.Backups) != 2 || cfg.Backups[1].Name != "media" || cfg.Backups[1].Storage != "disk" ||
		cfg.Instance.Revision != 2 || len(cfg.Instance.BackupDefaults) != 0 {
		t.Fatalf("config: backups %+v marker %+v", cfg.Backups, cfg.Instance)
	}
}

// Backups of a created instance are the app's own: they change in its
// Backups tab, not through an update.
func TestUpdateCreatedRefusesBackups(t *testing.T) {
	f := newFixture(t, nil)
	created(t, f)
	sent := f.disp.count()
	_, err := f.uc.UpdateInstance(dbtest.Ctx(t), "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Backups: map[string]string{"db": "disk"}})
	if !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "backups can only be set while the instance is pending") {
		t.Fatalf("err = %v", err)
	}
	if f.disp.count() != sent {
		t.Fatal("a refused update dispatched")
	}
}
