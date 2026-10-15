package contract_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	happ "winterflow/internal/app/web/handler/app"
	htpl "winterflow/internal/app/web/handler/templates"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/crypto/cryptotest"
)

// instanceOf reads get-instance; no answer may carry a secret.
func instanceOf(t *testing.T, c *client, serverID, key string) tuc.InstanceView {
	t.Helper()
	var v tuc.InstanceView
	r := c.get(t, "/api/v1/instance/get-instance", url.Values{"server_id": {serverID}, "key": {key}}).expect(t, http.StatusOK)
	noSecrets(t, string(r.Body))
	r.data(t, &v)
	return v
}

// instanceEnv is an owner whose server covers *.example.test and has the
// storage "disk", with template "shop" (shopBody) saved. secret is the
// ciphertext its creates send.
type instanceEnv struct {
	e      *env
	owner  *client
	sid    string
	tplID  string
	secret string
}

func newInstanceEnv(t *testing.T) instanceEnv {
	t.Helper()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	registerDomain(t, owner, sid, "*.example.test", model.DomainTLSOff)
	addStorage(t, owner, sid, model.Storage{Name: "disk", Type: model.StorageLocal, Local: &model.LocalStorage{Path: "/mnt/usb"}})
	saved := saveTemplate(t, owner, htpl.SaveTemplateRequest{Name: "shop", Body: shopBody(shopCompose)})
	return instanceEnv{e: e, owner: owner, sid: sid, tplID: saved.ID, secret: cryptotest.Ciphertext(t, "s3cret")}
}

// create is a waited create of key with NAME, the app name shop-<name>,
// the hostname <name>.example.test on app:8080 and backup db on "disk".
func (ie instanceEnv) create(key, name string) htpl.CreateInstanceRequest {
	return htpl.CreateInstanceRequest{
		ServerID: ie.sid, Key: key, TemplateID: ie.tplID, Name: "shop-" + name, Values: map[string]string{"NAME": name},
		Secrets: map[string]string{"SECRET_KEY": ie.secret},
		Hosts:   []model.InstanceHost{{Host: name + ".example.test", Expose: "app:8080"}},
		Backups: map[string]template.BackupChoice{"db": {Enabled: true, Storage: "disk"}}, Wait: true,
	}
}

// created creates key and returns the waited result.
func (ie instanceEnv) created(t *testing.T, key, name string) htpl.InstanceResult {
	t.Helper()
	var res htpl.InstanceResult
	r := ie.owner.post(t, "/api/v1/instance/create-instance", ie.create(key, name)).expect(t, http.StatusOK)
	noSecrets(t, string(r.Body))
	r.data(t, &res)
	return res
}

// lastSave is the last app.save the fake agent received.
func lastSave(t *testing.T, e *env) command.SaveAppRequest {
	t.Helper()
	saves := e.agent.seen(command.TypeAppSave)
	if len(saves) == 0 {
		t.Fatal("no app.save reached the agent")
	}
	return decode[command.SaveAppRequest](saves[len(saves)-1].Payload)
}

func TestInstanceCreate(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	e, owner, sid := ie.e, ie.owner, ie.sid

	res := ie.created(t, "tenant-1", "acme")
	appID := res.Instance.AppID
	t.Run("create with wait answers the instance", func(t *testing.T) {
		if res.Instance.State != "created" || res.Instance.Revision != 1 || res.Instance.Secrets["SECRET_KEY"] != command.EncryptedPlaceholder ||
			res.Instance.Values["NAME"] != "acme" || res.Instance.TemplateName != "shop" || res.Instance.Name != "shop-acme" ||
			len(res.Instance.Hosts) != 1 || res.RequestID == "" {
			t.Fatalf("result = %+v", res)
		}
		req := lastSave(t, e)
		var cfg map[string]json.RawMessage
		_ = json.Unmarshal(req.App.Config, &cfg)
		if req.App.AppID != appID || string(cfg["template_id"]) != `"`+ie.tplID+`"` ||
			string(cfg["backups"]) != `[{"name":"db","enabled":true,"storage":"disk"}]` ||
			string(cfg["ingress"]) != `{"domains":[{"domain":"acme.example.test","expose":"app:8080","ssl":false}],"redirects":[]}` {
			t.Fatalf("dispatched config = %s", req.App.Config)
		}
		// One app variable per value; VERSION was left to the compose file.
		if len(req.App.Variables) != 2 {
			t.Fatalf("variables = %+v", req.App.Variables)
		}
		apps := listApps(t, owner, sid)
		if len(apps) != 1 || apps[0].ID != appID || apps[0].Name != "shop-acme" || apps[0].TemplateID != ie.tplID || len(apps[0].Domains) != 1 {
			t.Fatalf("apps = %+v", apps)
		}
	})
	t.Run("get-instance shows the app's live status", func(t *testing.T) {
		body, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{{AppID: appID, StatusCode: command.ContainerStatusActive, Containers: []command.ContainerStatus{}}}})
		e.publishEvent(t, bus.EventMessage{ServerID: sid, Kind: bus.EventAppsStatus, Payload: body})
		eventually(t, func() bool { return instanceOf(t, owner, sid, "tenant-1").StatusCode == command.ContainerStatusActive })
	})
	t.Run("a replay makes no second app; other values are a conflict", func(t *testing.T) {
		n := len(e.agent.seen(command.TypeAppSave))
		var again htpl.InstanceResult
		replay := ie.create("tenant-1", "acme")
		replay.Secrets = map[string]string{"SECRET_KEY": cryptotest.Ciphertext(t, "s3cret")} // new bytes, the same secret
		owner.post(t, "/api/v1/instance/create-instance", replay).expect(t, http.StatusOK).data(t, &again)
		if again.Instance.AppID != appID || again.RequestID != "" || len(e.agent.seen(command.TypeAppSave)) != n {
			t.Fatalf("replay = %+v", again)
		}
		other := ie.create("tenant-1", "acme")
		other.Values["VERSION"] = "2.0"
		owner.post(t, "/api/v1/instance/create-instance", other).expect(t, http.StatusConflict)
	})
	t.Run("a host another app holds is refused before the key is claimed", func(t *testing.T) {
		owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-x", "acme")).expect(t, http.StatusBadRequest)
		owner.get(t, "/api/v1/instance/get-instance", url.Values{"server_id": {sid}, "key": {"tenant-x"}}).expect(t, http.StatusNotFound)
	})
	t.Run("without wait the result arrives over SSE", func(t *testing.T) {
		in := ie.create("tenant-2", "bravo")
		in.Wait = false
		var saved command.SaveAppResponse
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/instance/create-instance", Body: in}, &saved)
		if got := instanceOf(t, owner, sid, "tenant-2"); got.AppID != saved.AppID || got.State != "created" {
			t.Fatalf("tenant-2 = %+v (saved %+v)", got, saved)
		}
	})
	t.Run("a failed deploy answers 502; the same request retries it", func(t *testing.T) {
		e.agent.fail(command.TypeAppSave, "pull access denied")
		r := owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-3", "charlie")).expect(t, http.StatusBadGateway)
		if !strings.Contains(string(r.Body), "pull access denied") {
			t.Fatalf("502 body = %s", r.Body)
		}
		if got := instanceOf(t, owner, sid, "tenant-3"); got.State != "pending" || got.LastError != "pull access denied" {
			t.Fatalf("after a failure = %+v", got)
		}
		e.agent.on(command.TypeAppSave, defaultScript(command.TypeAppSave))
		var retried htpl.InstanceResult
		owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-3", "charlie")).expect(t, http.StatusOK).data(t, &retried)
		if retried.Instance.State != "created" || retried.Instance.LastError != "" {
			t.Fatalf("retried = %+v", retried.Instance)
		}
	})
	t.Run("list by template and by server", func(t *testing.T) {
		var list tuc.InstanceList
		owner.get(t, "/api/v1/instance/get-instances", url.Values{"template_id": {ie.tplID}}).expect(t, http.StatusOK).data(t, &list)
		if len(list.Instances) != 3 || list.Instances[0].Key != "tenant-1" {
			t.Fatalf("by template = %+v", list)
		}
		owner.get(t, "/api/v1/instance/get-instances", url.Values{"server_id": {sid}}).expect(t, http.StatusOK).data(t, &list)
		if len(list.Instances) != 3 {
			t.Fatalf("by server = %+v", list)
		}
		var tpls tuc.TemplateList
		owner.get(t, "/api/v1/template/get-templates", nil).expect(t, http.StatusOK).data(t, &tpls)
		if tpls.Templates[0].Instances != 3 {
			t.Fatalf("templates = %+v", tpls)
		}
	})
	t.Run("values and hostnames are checked", func(t *testing.T) {
		bad := ie.create("tenant-4", "UPPER")
		bad.Hosts = nil
		r := owner.post(t, "/api/v1/instance/create-instance", bad).expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), "variable NAME") {
			t.Fatalf("body = %s", r.Body)
		}
		bad = ie.create("tenant-4", "delta")
		bad.Secrets = nil
		owner.post(t, "/api/v1/instance/create-instance", bad).expect(t, http.StatusBadRequest)
		bad = ie.create("tenant-4", "delta")
		bad.Hosts = []model.InstanceHost{{Host: "delta.example.test", Expose: "app:9999"}}
		r = owner.post(t, "/api/v1/instance/create-instance", bad).expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), "hosts[0].expose") {
			t.Fatalf("body = %s", r.Body)
		}
		bad = ie.create("tenant-4", "delta")
		bad.Hosts = []model.InstanceHost{{Host: "delta.elsewhere.test", Expose: "app:8080"}}
		owner.post(t, "/api/v1/instance/create-instance", bad).expect(t, http.StatusBadRequest)
	})
}

// Another org's template and keys read as unknown.
func TestInstancesOfAnotherOrgAreNotFound(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	ie.created(t, "tenant-1", "acme")
	mallory, foreign := ie.e.foreignClient(t)
	in := ie.create("k", "acme")
	in.ServerID = foreign
	mallory.post(t, "/api/v1/instance/create-instance", in).expect(t, http.StatusNotFound)
	mallory.get(t, "/api/v1/instance/get-instances", url.Values{"template_id": {ie.tplID}}).expect(t, http.StatusNotFound)
	mallory.get(t, "/api/v1/instance/get-instance", url.Values{"server_id": {foreign}, "key": {"tenant-1"}}).expect(t, http.StatusNotFound)
	var list tuc.InstanceList
	mallory.get(t, "/api/v1/instance/get-instances", nil).expect(t, http.StatusOK).data(t, &list)
	if len(list.Instances) != 0 {
		t.Fatalf("mallory sees %+v", list)
	}
	ie.owner.get(t, "/api/v1/instance/get-instance", url.Values{"server_id": {ie.sid}, "key": {"nope"}}).expect(t, http.StatusNotFound)
}

func TestInstanceUpdateAndUpgrade(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	e, owner, sid := ie.e, ie.owner, ie.sid
	ie.created(t, "tenant-1", "acme")

	t.Run("update without wait answers 202 and keeps the secret and the hosts", func(t *testing.T) {
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/instance/update-instance",
			Body: htpl.UpdateInstanceRequest{ServerID: sid, Key: "tenant-1", Values: map[string]string{"VERSION": "2.0"}}}, nil)
		got := instanceOf(t, owner, sid, "tenant-1")
		if got.Values["VERSION"] != "2.0" || got.Values["NAME"] != "acme" || len(got.Hosts) != 1 {
			t.Fatalf("after update = %+v", got)
		}
		save := lastSave(t, e)
		for _, v := range save.App.Variables {
			if v.Name == "SECRET_KEY" && string(v.Content) != command.EncryptedPlaceholder {
				t.Fatalf("an update re-sent the secret: %q", v.Content)
			}
		}
		if strings.Contains(string(save.App.Config), `"backups"`) || !strings.Contains(string(save.App.Config), `"acme.example.test"`) {
			t.Fatalf("an update's config: %s", save.App.Config)
		}
	})
	t.Run("update replaces the hosts when given", func(t *testing.T) {
		hosts := []model.InstanceHost{{Host: "shop.example.test", Expose: "app:8080"}}
		var res htpl.InstanceResult
		owner.post(t, "/api/v1/instance/update-instance", htpl.UpdateInstanceRequest{ServerID: sid, Key: "tenant-1", Hosts: &hosts, Wait: true}).
			expect(t, http.StatusOK).data(t, &res)
		if len(res.Instance.Hosts) != 1 || res.Instance.Hosts[0].Host != "shop.example.test" {
			t.Fatalf("hosts = %+v", res.Instance.Hosts)
		}
	})
	t.Run("update checks the values", func(t *testing.T) {
		owner.post(t, "/api/v1/instance/update-instance", htpl.UpdateInstanceRequest{ServerID: sid, Key: "tenant-1",
			Values: map[string]string{"NOPE": "1"}}).expect(t, http.StatusBadRequest)
		owner.post(t, "/api/v1/instance/update-instance", htpl.UpdateInstanceRequest{ServerID: sid, Key: "nope"}).
			expect(t, http.StatusNotFound)
	})
	t.Run("a created instance's backups are changed in the Backups tab", func(t *testing.T) {
		for _, path := range []string{"/api/v1/instance/update-instance", "/api/v1/instance/upgrade-instance"} {
			r := owner.post(t, path, map[string]any{"server_id": sid, "key": "tenant-1", "revision": 1,
				"backups": map[string]any{"db": map[string]any{"enabled": true, "storage": "disk"}}}).expect(t, http.StatusBadRequest)
			if !strings.Contains(string(r.Body), "backups can only be set while the instance is pending") {
				t.Fatalf("%s body = %s", path, r.Body)
			}
		}
	})
	t.Run("upgrade keeps the values and sends no backups list", func(t *testing.T) {
		next := strings.Replace(shopCompose, "path: shop.db }\n", "path: shop.db }\n    - { name: media, type: files, volume: data }\n", 1)
		if saved := saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: ie.tplID, Name: "shop", Body: shopBody(next), BaseRevision: 1}); saved.Revision != 2 {
			t.Fatalf("saved = %+v", saved)
		}
		var res htpl.InstanceResult
		owner.post(t, "/api/v1/instance/upgrade-instance", htpl.UpgradeInstanceRequest{ServerID: sid, Key: "tenant-1", Revision: 2, Wait: true}).
			expect(t, http.StatusOK).data(t, &res)
		if res.Instance.Revision != 2 || res.Instance.Values["VERSION"] != "2.0" {
			t.Fatalf("upgraded = %+v", res.Instance)
		}
		var cfg struct {
			Backups  json.RawMessage `json:"backups"`
			Instance template.Marker `json:"instance"`
		}
		_ = json.Unmarshal(lastSave(t, e).App.Config, &cfg)
		if cfg.Backups != nil || cfg.Instance.Revision != 2 {
			t.Fatalf("upgrade config = %s", lastSave(t, e).App.Config)
		}
	})
	t.Run("an upgrade a newly required value is missing for is refused", func(t *testing.T) {
		next := strings.NewReplacer(
			"      VERSION: \"1.0\"\n", "      VERSION: \"1.0\"\n      REGION: \"\"\n",
			"    VERSION: {}\n", "    VERSION: {}\n    REGION: { required: true }\n",
		).Replace(shopCompose)
		saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: ie.tplID, Name: "shop", Body: shopBody(next), BaseRevision: 2})
		r := owner.post(t, "/api/v1/instance/upgrade-instance", htpl.UpgradeInstanceRequest{ServerID: sid, Key: "tenant-1", Revision: 3}).
			expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), "variable REGION: a value is required") {
			t.Fatalf("body = %s", r.Body)
		}
		owner.post(t, "/api/v1/instance/upgrade-instance", htpl.UpgradeInstanceRequest{ServerID: sid, Key: "tenant-1", Revision: 3,
			Values: map[string]string{"REGION": "eu"}, Wait: true}).expect(t, http.StatusOK)
		owner.post(t, "/api/v1/instance/upgrade-instance", htpl.UpgradeInstanceRequest{ServerID: sid, Key: "tenant-1", Revision: 9}).
			expect(t, http.StatusNotFound)
	})
}

// A pending instance (its first save failed) was never confirmed: the agent
// holds none of its secrets and no backups list, so an update sends both.
func TestInstanceUpdatePending(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	e, owner, sid := ie.e, ie.owner, ie.sid

	e.agent.fail(command.TypeAppSave, "pull access denied")
	owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-p", "pending")).expect(t, http.StatusBadGateway)
	if got := instanceOf(t, owner, sid, "tenant-p"); got.State != "pending" || got.Name != "shop-pending" {
		t.Fatalf("tenant-p = %+v", got)
	}
	e.agent.on(command.TypeAppSave, defaultScript(command.TypeAppSave))

	r := owner.post(t, "/api/v1/instance/update-instance", htpl.UpdateInstanceRequest{ServerID: sid, Key: "tenant-p",
		Values: map[string]string{"VERSION": "3.0"}}).expect(t, http.StatusBadRequest)
	if !strings.Contains(string(r.Body), "variable SECRET_KEY: a value is required") {
		t.Fatalf("a pending update without the secret: %s", r.Body)
	}
	r = owner.post(t, "/api/v1/instance/update-instance", htpl.UpdateInstanceRequest{ServerID: sid, Key: "tenant-p",
		Secrets: map[string]string{"SECRET_KEY": "s3cret"}}).expect(t, http.StatusBadRequest)
	if !strings.Contains(string(r.Body), "secrets.SECRET_KEY: not a ciphertext") {
		t.Fatalf("a plaintext secret: %s", r.Body)
	}
	again := cryptotest.Ciphertext(t, "s3cret")
	var res htpl.InstanceResult
	owner.post(t, "/api/v1/instance/update-instance", htpl.UpdateInstanceRequest{ServerID: sid, Key: "tenant-p",
		Values: map[string]string{"VERSION": "3.0"}, Secrets: map[string]string{"SECRET_KEY": again},
		Backups: map[string]template.BackupChoice{"db": {Enabled: true, Storage: "disk"}}, Wait: true}).expect(t, http.StatusOK).data(t, &res)
	if res.Instance.State != "created" || res.Instance.Values["VERSION"] != "3.0" {
		t.Fatalf("updated = %+v", res.Instance)
	}
	save := lastSave(t, e)
	for _, v := range save.App.Variables {
		if v.Name == "SECRET_KEY" && (string(v.Content) != again || !v.Encrypted) {
			t.Fatalf("the secret = %+v", v)
		}
	}
	var cfg struct {
		Name    string                           `json:"name"`
		Backups []struct{ Name, Storage string } `json:"backups"`
	}
	_ = json.Unmarshal(save.App.Config, &cfg)
	if cfg.Name != "shop-pending" || len(cfg.Backups) != 1 || cfg.Backups[0].Name != "db" || cfg.Backups[0].Storage != "disk" {
		t.Fatalf("a pending update's config = %s", save.App.Config)
	}
}

// A catalog template is installed in the caller's org; two orgs upgrading
// it don't block each other.
func TestCatalogInstances(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	e, owner, sid := ie.e, ie.owner, ie.sid
	cat := dbtest.ID(902)
	catalogTemplate(t, e, cat, "shop", shopBody(shopCompose))
	in := ie.create("cat-1", "cata")
	in.TemplateID, in.Name = cat, ""
	var res htpl.InstanceResult
	owner.post(t, "/api/v1/instance/create-instance", in).expect(t, http.StatusOK).data(t, &res)
	if res.Instance.TemplateID != cat || res.Instance.Name != "Shop" {
		t.Fatalf("created = %+v", res.Instance)
	}
	mallory, foreign := e.foreignClient(t)
	mine := htpl.CreateInstanceRequest{ServerID: foreign, Key: "cat-1", TemplateID: cat, Values: map[string]string{"NAME": "mall"},
		Secrets: map[string]string{"SECRET_KEY": ie.secret}, Wait: true}
	mallory.post(t, "/api/v1/instance/create-instance", mine).expect(t, http.StatusOK)
	for _, c := range []*client{owner, mallory} {
		var v model.RolloutView
		c.post(t, "/api/v1/template/upgrade-instances", htpl.UpgradeInstancesRequest{ID: cat}).expect(t, http.StatusOK).data(t, &v)
		if v.ScopeKey != cat {
			t.Fatalf("queued = %+v", v)
		}
	}
	var list tuc.InstanceList
	owner.get(t, "/api/v1/instance/get-instances", url.Values{"template_id": {cat}}).expect(t, http.StatusOK).data(t, &list)
	if len(list.Instances) != 1 || list.Instances[0].ServerID != sid {
		t.Fatalf("the owner's instances of the catalog template = %+v", list)
	}
}

func TestInstanceLifecycle(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	e, owner, sid := ie.e, ie.owner, ie.sid
	appID := ie.created(t, "tenant-1", "acme").Instance.AppID

	t.Run("control", func(t *testing.T) {
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/instance/control-instance",
			Body: htpl.ControlInstanceRequest{ServerID: sid, Key: "tenant-1", Action: command.ControlRestart}}, nil)
		owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/instance/control-instance", Invalid: true,
			Body: htpl.ControlInstanceRequest{ServerID: sid, Key: "tenant-1", Action: command.ControlUpdate}}).expect(t, http.StatusBadRequest)
		e.agent.fail(command.TypeAppControl, "docker compose stop: boom")
		r := owner.post(t, "/api/v1/instance/control-instance", htpl.ControlInstanceRequest{ServerID: sid, Key: "tenant-1", Action: command.ControlStop, Wait: true}).
			expect(t, http.StatusBadGateway)
		if !strings.Contains(string(r.Body), "boom") || instanceOf(t, owner, sid, "tenant-1").LastError != "docker compose stop: boom" {
			t.Fatalf("502 body = %s", r.Body)
		}
	})
	t.Run("a template with live instances is not deleted", func(t *testing.T) {
		owner.post(t, "/api/v1/template/delete-template", htpl.TemplateIDRequest{ID: ie.tplID}).expect(t, http.StatusConflict)
	})
	t.Run("unlink frees the key", func(t *testing.T) {
		var res htpl.InstanceResult
		owner.post(t, "/api/v1/instance/unlink-instance", htpl.InstanceKeyRequest{ServerID: sid, Key: "tenant-1", Wait: true}).
			expect(t, http.StatusOK).data(t, &res)
		if res.Instance.State != "unlinked" || res.Instance.AppID != appID {
			t.Fatalf("unlinked = %+v", res.Instance)
		}
		cfg := string(lastSave(t, e).App.Config)
		if strings.Contains(cfg, `"instance"`) || strings.Contains(cfg, `"template_id"`) {
			t.Fatalf("unlink config = %s", cfg)
		}
		owner.get(t, "/api/v1/instance/get-instance", url.Values{"server_id": {sid}, "key": {"tenant-1"}}).expect(t, http.StatusNotFound)
		if apps := listApps(t, owner, sid); apps[0].ID != appID || apps[0].TemplateID != "" {
			t.Fatalf("unlinked app = %+v", apps[0])
		}
	})
	t.Run("a freed key makes a new app; delete retires it", func(t *testing.T) {
		again := ie.created(t, "tenant-1", "bravo")
		if again.Instance.AppID == appID || again.RequestID == "" {
			t.Fatalf("a freed key took over the unlinked app: %+v", again)
		}
		var res htpl.InstanceResult
		owner.post(t, "/api/v1/instance/delete-instance", htpl.DeleteInstanceRequest{ServerID: sid, Key: "tenant-1", DeleteVolumes: true, Wait: true}).
			expect(t, http.StatusOK).data(t, &res)
		if res.Instance.State != "deleted" {
			t.Fatalf("deleted = %+v", res.Instance)
		}
		dels := e.agent.seen(command.TypeAppDelete)
		if req := decode[command.DeleteAppRequest](dels[len(dels)-1].Payload); req.AppID != again.Instance.AppID || !req.DeleteVolumes {
			t.Fatalf("app.delete = %+v", req)
		}
		owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-1", "bravo")).expect(t, http.StatusConflict)
		n := len(e.agent.seen(command.TypeAppDelete))
		owner.post(t, "/api/v1/instance/delete-instance", htpl.DeleteInstanceRequest{ServerID: sid, Key: "tenant-1"}).expect(t, http.StatusOK)
		if len(e.agent.seen(command.TypeAppDelete)) != n {
			t.Fatal("deleting a deleted instance reached the agent")
		}
		owner.post(t, "/api/v1/instance/unlink-instance", htpl.InstanceKeyRequest{ServerID: sid, Key: "tenant-1"}).expect(t, http.StatusConflict)
	})
	t.Run("without live instances the template is deleted", func(t *testing.T) {
		owner.post(t, "/api/v1/template/delete-template", htpl.TemplateIDRequest{ID: ie.tplID}).expect(t, http.StatusOK)
	})
}

// The app routes leave an instance's app to its instance: save-app and
// rollback-app answer 409, rename goes on, and delete-app deletes the
// instance (its key stays retired).
func TestInstanceAppThroughTheAppRoutes(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	e, owner, sid := ie.e, ie.owner, ie.sid
	appID := ie.created(t, "tenant-1", "acme").Instance.AppID
	saves := len(e.agent.seen(command.TypeAppSave))

	r := owner.post(t, "/api/v1/app/save-app", happ.SaveAppRequest{ServerID: sid, App: model.App{ID: appID, Name: "hijack"},
		Config: json.RawMessage(`{}`)}).expect(t, http.StatusConflict)
	if !strings.Contains(string(r.Body), "this app is managed by template shop: update the instance or unlink it") {
		t.Fatalf("save-app body = %s", r.Body)
	}
	owner.post(t, "/api/v1/app/rollback-app", happ.RollbackAppRequest{ServerID: sid, AppID: appID, Hash: "rev-1"}).
		expect(t, http.StatusConflict)
	if len(e.agent.seen(command.TypeAppSave)) != saves || len(e.agent.seen(command.TypeAppRollback)) != 0 {
		t.Fatal("a refused call reached the agent")
	}
	owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/rename-app",
		Body: happ.RenameAppRequest{ServerID: sid, AppID: appID, Name: "renamed"}}, nil)

	owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/delete-app",
		Body: happ.DeleteAppRequest{ServerID: sid, AppID: appID}}, nil)
	eventually(t, func() bool { return instanceOf(t, owner, sid, "tenant-1").State == "deleted" })
	r = owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-1", "acme")).expect(t, http.StatusConflict)
	if !strings.Contains(string(r.Body), "this key belonged to a deleted instance") {
		t.Fatalf("replay body = %s", r.Body)
	}
	owner.post(t, "/api/v1/template/delete-template", htpl.TemplateIDRequest{ID: ie.tplID}).expect(t, http.StatusOK)
}

// A member of two orgs acts for their primary one (the oldest membership):
// the other org's server is theirs to see, but no instance may sit on it.
func TestInstanceRoutesStayInTheCallersOrg(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	other := e.foreignServer(t)
	orgB := dbtest.QueryString(t, e.conn, `SELECT organization_id FROM servers WHERE server_id = ?`, other)
	dbtest.Exec(t, e.conn, `INSERT INTO organization_users (organization_id, user_id, role, created_at) VALUES (?, ?, 'admin', '2099-01-01 00:00:00')`,
		orgB, owner.userID)
	owner.get(t, "/api/v1/app/get-apps", sq(other)).expect(t, http.StatusOK)
	owner.get(t, "/api/v1/instance/get-instances", sq(other)).expect(t, http.StatusForbidden)
	owner.post(t, "/api/v1/instance/create-instance", htpl.CreateInstanceRequest{ServerID: other, Key: "k", TemplateID: anApp}).
		expect(t, http.StatusForbidden)
	if n := e.agent.count(); n != 0 {
		t.Fatalf("%d command(s) reached an agent", n)
	}
}
