package contract_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	happ "winterflow/internal/app/web/handler/app"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/bus"
)

// storagesIndexed is the API's server_storages index for serverID.
func (e *env) storagesIndexed(t *testing.T, serverID string) map[string]model.StorageSummary {
	t.Helper()
	rows, err := e.deps.ServerStorageRepository.ListForServer(e.dbCtx(t), serverID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]model.StorageSummary{}
	for _, r := range rows {
		out[r.Name] = r
	}
	return out
}

// addStorage saves a storage on serverID through the API.
func addStorage(t *testing.T, c *client, serverID string, s model.Storage) {
	t.Helper()
	c.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/storage/save", Query: sq(serverID),
		Body: command.SaveStorageRequest{Storage: s, Create: true}}, nil)
}

func hetzner(secret string) model.Storage {
	return model.Storage{Name: "hetzner", Type: model.StorageS3, S3: &model.S3Storage{
		Endpoint: "https://fsn1.example.test", Region: "fsn1", Bucket: "tenants", Prefix: "wf",
		AccessKeyID: "AKIA", SecretAccessKey: secret,
	}}
}

func TestStorageRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	q := url.Values{"server_id": {sid}}

	t.Run("test reports the probe and stores nothing", func(t *testing.T) {
		var res command.TestStorageResponse
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/storage/test", Query: q,
			Body: command.TestStorageRequest{Storage: hetzner("BASE64CIPHERTEXT")}}, &res)
		if !res.OK {
			t.Fatalf("test = %+v", res)
		}
		e.agent.on(command.TypeStorageTest, func(json.RawMessage) reply {
			return reply{Payload: command.TestStorageResponse{Error: "write a test file: Access Denied"}}
		})
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/storage/test", Query: q,
			Body: command.TestStorageRequest{Storage: hetzner(model.EncryptedSecret)}}, &res)
		if res.OK || res.Error != "write a test file: Access Denied" {
			t.Fatalf("failed probe = %+v", res)
		}
		if len(e.storagesIndexed(t, sid)) != 0 {
			t.Fatal("a test entered the index")
		}
	})
	t.Run("save enters the index without its secret", func(t *testing.T) {
		var saved command.SaveStorageResponse
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/storage/save", Query: q,
			Body: command.SaveStorageRequest{Storage: hetzner("BASE64CIPHERTEXT"), Create: true}}, &saved)
		if saved.Storage.Name != "hetzner" || saved.Storage.S3.SecretAccessKey != model.EncryptedSecret ||
			saved.Storage.Location != "https://fsn1.example.test/tenants/wf/" {
			t.Fatalf("saved = %+v", saved.Storage)
		}
		got := e.agent.seen(command.TypeStorageSave)
		if in := decode[command.SaveStorageRequest](got[len(got)-1].Payload); in.S3.SecretAccessKey != "BASE64CIPHERTEXT" {
			t.Fatalf("the secret reached the agent as %q", in.S3.SecretAccessKey)
		}
		idx := e.storagesIndexed(t, sid)
		if idx["hetzner"] != (model.StorageSummary{Name: "hetzner", Type: model.StorageS3, Location: "https://fsn1.example.test/tenants/wf/"}) {
			t.Fatalf("index = %+v", idx)
		}
	})
	t.Run("an add never replaces and an edit never creates", func(t *testing.T) {
		owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/storage/save", Query: q,
			Body: command.SaveStorageRequest{Storage: hetzner("OTHER"), Create: true}}, "a storage named hetzner already exists on this server")
		owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/storage/save", Query: q,
			Body: command.SaveStorageRequest{Storage: model.Storage{Name: "nope", Type: model.StorageLocal, Local: &model.LocalStorage{Path: "/mnt/nope"}}}},
			"there is no storage nope on this server")
		// The flag travels as "create"; an edit omits it or sends false.
		got := e.agent.seen(command.TypeStorageSave)
		if !strings.Contains(string(got[len(got)-2].Payload), `"create":true`) || !strings.Contains(string(got[len(got)-1].Payload), `"create":false`) {
			t.Fatalf("payloads = %s / %s", got[len(got)-2].Payload, got[len(got)-1].Payload)
		}
		var saved command.SaveStorageResponse
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/storage/save", Query: q,
			Body: command.SaveStorageRequest{Storage: hetzner(model.EncryptedSecret)}}, &saved)
		if saved.Storage.Name != "hetzner" || len(e.storagesIndexed(t, sid)) != 1 {
			t.Fatalf("edit = %+v", saved)
		}
	})
	t.Run("list reports the agent's registry and reconciles", func(t *testing.T) {
		e.agent.addStorage(sid, command.StorageInfo{
			Storage:  model.Storage{Name: "disk", Type: model.StorageLocal, Local: &model.LocalStorage{Path: "/mnt/usb"}},
			Location: "/mnt/usb", Apps: []string{"a1"},
		})
		var listed command.ListStoragesResponse
		owner.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/storage/list", Query: q}, &listed)
		if len(listed.Storages) != 2 || listed.Storages[0].Name != "disk" || listed.Storages[0].Apps[0] != "a1" {
			t.Fatalf("listed = %+v", listed)
		}
		if idx := e.storagesIndexed(t, sid); len(idx) != 2 || idx["disk"].Location != "/mnt/usb" {
			t.Fatalf("index = %+v", idx)
		}
	})
	t.Run("an agent coming online fills the index", func(t *testing.T) {
		e.agent.addStorage(sid, command.StorageInfo{
			Storage:  model.Storage{Name: "nas", Type: model.StorageLocal, Local: &model.LocalStorage{Path: "/media/nas"}},
			Location: "/media/nas",
		})
		n := len(e.agent.seen(command.TypeStorageList))
		e.publishEvent(t, bus.EventMessage{ServerID: sid, Kind: bus.EventCapabilities, Payload: []byte(`{"capabilities":{}}`)})
		eventually(t, func() bool { return e.storagesIndexed(t, sid)["nas"].Location == "/media/nas" })
		if got := e.agent.seen(command.TypeStorageList); len(got) != n+1 {
			t.Fatalf("storage.list dispatches = %d, want one more than %d", len(got), n)
		}
	})
	t.Run("get-storages reads the index, never a secret", func(t *testing.T) {
		r := owner.get(t, "/api/v1/storage/get-storages", q).expect(t, http.StatusOK)
		var got struct {
			Storages []model.StorageSummary `json:"storages"`
		}
		r.data(t, &got)
		if len(got.Storages) != 3 || got.Storages[0].Name != "disk" || got.Storages[1].Name != "hetzner" {
			t.Fatalf("get-storages = %+v", got.Storages)
		}
		if strings.Contains(string(r.Body), "CIPHERTEXT") || strings.Contains(string(r.Body), "secret") {
			t.Fatalf("a secret field leaked: %s", r.Body)
		}
	})
	t.Run("delete leaves the index", func(t *testing.T) {
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/storage/delete", Query: q,
			Body: command.DeleteStorageRequest{Name: "nas"}}, nil)
		if _, ok := e.storagesIndexed(t, sid)["nas"]; ok {
			t.Fatal("deleted storage still indexed")
		}
	})
	t.Run("a refused delete keeps the storage", func(t *testing.T) {
		e.agent.fail(command.TypeStorageDelete, "disk is used by Shop: remove its WF_STORAGE_ variables from those apps first")
		owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/storage/delete", Query: q,
			Body: command.DeleteStorageRequest{Name: "disk"}}, "disk is used by Shop: remove its WF_STORAGE_ variables from those apps first")
		if _, ok := e.storagesIndexed(t, sid)["disk"]; !ok {
			t.Fatal("a refused delete left the index")
		}
	})
	t.Run("a failed save changes nothing", func(t *testing.T) {
		e.agent.fail(command.TypeStorageSave, "the connection test failed, nothing was saved: the storage folder /mnt/gone is missing (is the disk mounted?)")
		owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/storage/save", Query: q,
			Body: command.SaveStorageRequest{Storage: model.Storage{Name: "gone", Type: model.StorageLocal, Local: &model.LocalStorage{Path: "/mnt/gone"}}}},
			"the connection test failed, nothing was saved: the storage folder /mnt/gone is missing (is the disk mounted?)")
		if _, ok := e.storagesIndexed(t, sid)["gone"]; ok {
			t.Fatal("a failed save entered the index")
		}
	})
	t.Run("the API rejects a bad storage before the agent", func(t *testing.T) {
		n := e.agent.count()
		for _, body := range []any{
			map[string]any{"name": "Disk", "type": "local", "local": map[string]any{"path": "/mnt"}},
			map[string]any{"name": "disk", "type": "local", "local": map[string]any{"path": "relative"}},
			map[string]any{"name": "s", "type": "s3", "s3": map[string]any{"endpoint": "http://s3.example.test", "bucket": "bkt", "access_key_id": "A", "secret_access_key": "C"}},
		} {
			owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/storage/save", Query: q, Body: body}).expect(t, http.StatusBadRequest)
		}
		if e.agent.count() != n {
			t.Fatal("an invalid storage reached the agent")
		}
	})
}

func TestSaveAppStorageVariables(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	save := func(compose string, vars ...happ.ContentItemRequest) *response {
		return owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
			ServerID: sid, App: model.App{Name: "litestream", Icon: "box", Color: "#112233"}, Config: json.RawMessage(`{}`),
			Files:     []happ.ContentItemRequest{{Name: "compose.yml", Content: compose}},
			Variables: vars,
		}})
	}
	const compose = "services:\n  ls:\n    environment:\n      BUCKET: ${WF_STORAGE_HETZNER_BUCKET}\n"

	r := save(compose).expect(t, http.StatusBadRequest)
	if !strings.Contains(string(r.Body), `${WF_STORAGE_HETZNER_BUCKET}: there is no storage \"hetzner\" on this server`) {
		t.Fatalf("body = %s", r.Body)
	}
	r = save("services: {}\n", happ.ContentItemRequest{Name: "WF_STORAGE_HETZNER_BUCKET", Content: "mine"}).expect(t, http.StatusBadRequest)
	if !strings.Contains(string(r.Body), "variables starting with WF_STORAGE_ or WF_BACKUP_ are provided by the server's storages") {
		t.Fatalf("body = %s", r.Body)
	}
	r = save("x: ${WF_BACKUP_DB_PATH}\n").expect(t, http.StatusBadRequest)
	if !strings.Contains(string(r.Body), `there is no backup \"db\" in this app`) {
		t.Fatalf("body = %s", r.Body)
	}
	if n := len(e.agent.seen(command.TypeAppSave)); n != 0 {
		t.Fatalf("%d refused save(s) reached the agent", n)
	}

	addStorage(t, owner, sid, hetzner("BASE64CIPHERTEXT"))
	owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
		ServerID: sid, App: model.App{Name: "litestream", Icon: "box", Color: "#112233"}, Config: json.RawMessage(`{}`),
		Files: []happ.ContentItemRequest{{Name: "compose.yml", Content: compose}},
	}}, nil)
}
