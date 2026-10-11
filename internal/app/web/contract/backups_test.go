package contract_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	happ "winterflow/internal/app/web/handler/app"
	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/bus"
)

func TestBackupRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	addStorage(t, owner, sid, model.Storage{Name: "disk", Type: model.StorageLocal, Local: &model.LocalStorage{Path: "/mnt/usb"}})

	t.Run("update-backups sends the whole list", func(t *testing.T) {
		var res command.UpdateBackupsResponse
		owner.succeed(t, post("/api/v1/app/update-backups", happ.UpdateBackupsRequest{ServerID: sid, UpdateBackupsRequest: command.UpdateBackupsRequest{
			AppID: anApp, Backups: []recipe.BackupConfig{{Name: "database", Enabled: true, Storage: "disk", Every: "15m"}, {Name: "media"}},
		}}), &res)
		if res.AppID != anApp || res.Revision != "rev-2" {
			t.Fatalf("result = %+v", res)
		}
		got := e.agent.seen(command.TypeBackupUpdate)
		in := decode[command.UpdateBackupsRequest](got[len(got)-1].Payload)
		if len(in.Backups) != 2 || in.Backups[0].Every != "15m" || !in.Backups[0].Enabled || in.Backups[1].Name != "media" {
			t.Fatalf("agent got %+v", in)
		}
	})
	t.Run("update-backups refuses before the agent", func(t *testing.T) {
		n := e.agent.count()
		for body, want := range map[string]string{
			`{"server_id":"` + sid + `","app_id":"` + anApp + `","backups":[{"name":"database","enabled":true,"storage":"nas"}]}`: `backup database: there is no storage \"nas\" on this server`,
			`{"server_id":"` + sid + `","app_id":"` + anApp + `","backups":[{"name":"database","enabled":true}]}`:                 "backups[0].storage: an enabled backup needs a storage",
			`{"server_id":"` + sid + `","app_id":"` + anApp + `","backups":[{"name":"Data_Base"}]}`:                               "is not a valid backup name",
			`{"app_id":"` + anApp + `","backups":[]}`:                                                                             "server_id is required",
			`{"server_id":"` + sid + `","app_id":"../x","backups":[]}`:                                                            "invalid app id",
		} {
			r := owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/update-backups", Body: body}).expect(t, http.StatusBadRequest)
			if !strings.Contains(string(r.Body), want) {
				t.Errorf("%s: body = %s, want %q", body, r.Body, want)
			}
		}
		if e.agent.count() != n {
			t.Fatal("a refused update reached the agent")
		}
	})
	t.Run("an agent refusal is the operation's error", func(t *testing.T) {
		e.agent.fail(command.TypeBackupUpdate, `backup media: /etc/app is not on a mount of service web`)
		owner.failWith(t, post("/api/v1/app/update-backups", map[string]any{"server_id": sid, "app_id": anApp, "backups": []any{}}),
			`backup media: /etc/app is not on a mount of service web`)
	})
	t.Run("list-restore-points", func(t *testing.T) {
		var res command.ListRestorePointsResponse
		owner.succeed(t, get("/api/v1/app/list-restore-points", url.Values{"server_id": {sid}, "app_id": {anApp}, "backup": {"database"}}), &res)
		if res.Backup != "database" || len(res.Points) != 1 || res.Points[0].Kind != command.PointScheduled {
			t.Fatalf("points = %+v", res)
		}
		for _, q := range []url.Values{
			{"server_id": {sid}, "app_id": {anApp}},
			{"server_id": {sid}, "app_id": {"a/b"}, "backup": {"database"}},
			{"app_id": {anApp}, "backup": {"database"}},
		} {
			owner.do(t, call{Method: http.MethodGet, Path: "/api/v1/app/list-restore-points", Query: q, Invalid: true}).expect(t, http.StatusBadRequest)
		}
	})
	t.Run("run-backup answers when the run ends", func(t *testing.T) {
		var res command.RunBackupResponse
		owner.succeed(t, post("/api/v1/app/run-backup", map[string]any{"server_id": sid, "app_id": anApp, "backup": "database"}), &res)
		if res.Unchanged || res.Point == nil || res.Point.Kind != command.PointManual {
			t.Fatalf("run = %+v", res)
		}
		e.agent.fail(command.TypeBackupRun, "database file not found: /src/0/app.db")
		owner.failWith(t, post("/api/v1/app/run-backup", map[string]any{"server_id": sid, "app_id": anApp, "backup": "database"}),
			"database file not found: /src/0/app.db")
	})
	t.Run("delete-restore-point", func(t *testing.T) {
		var res command.DeleteRestorePointResponse
		owner.succeed(t, post("/api/v1/app/delete-restore-point", map[string]any{
			"server_id": sid, "app_id": anApp, "backup": "database", "point": "20261011T120000Z-scheduled"}), &res)
		if res.Point != "20261011T120000Z-scheduled" {
			t.Fatalf("deleted = %+v", res)
		}
		r := owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/delete-restore-point", Invalid: true,
			Body: map[string]any{"server_id": sid, "app_id": anApp, "backup": "database", "point": "../../x"}}).expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), "invalid restore point id") {
			t.Fatalf("body = %s", r.Body)
		}
	})
}

// Save-app checks the compose file's backups recipe and its WF_BACKUP_
// references when the compose file is in the request.
func TestSaveAppBackups(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	const compose = `services:
  web:
    image: example/web
    volumes: [data:/data]
  litestream:
    image: litestream/litestream
    profiles: [wf-backup-replica]
    environment:
      BUCKET: ${WF_BACKUP_REPLICA_BUCKET}
volumes:
  data:
x-winterflow:
  backups:
    - {name: database, type: sqlite, volume: data, path: app.db}
    - {name: replica, type: custom, service: litestream}
`
	save := func(compose, config string) *response {
		return owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
			ServerID: sid, App: model.App{Name: "tenant", Icon: "box", Color: "#112233"}, Config: json.RawMessage(config),
			Files: []happ.ContentItemRequest{{Name: "compose.yml", Content: compose}},
		}})
	}
	for name, tc := range map[string]struct{ compose, config, want string }{
		"invalid recipe":      {strings.Replace(compose, "volume: data", "volume: gone", 1), `{}`, `x-winterflow.backups[0].volume: \"gone\" is not a volume of this compose file`},
		"unknown key":         {strings.Replace(compose, "path: app.db", "path: app.db, enabled: true", 1), `{}`, "a recipe never turns a backup on"},
		"undeclared backup":   {compose + "# ${WF_BACKUP_AUDIT_PATH}\n", `{}`, `there is no backup \"audit\" in this app`},
		"enabled, no storage": {compose, `{"backups":[{"name":"replica","enabled":true,"storage":"nas"}]}`, `backup replica: there is no storage \"nas\" on this server`},
	} {
		r := save(tc.compose, tc.config).expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), tc.want) {
			t.Errorf("%s: body = %s, want %q", name, r.Body, tc.want)
		}
	}
	if n := len(e.agent.seen(command.TypeAppSave)); n != 0 {
		t.Fatalf("%d refused save(s) reached the agent", n)
	}
	owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
		ServerID: sid, App: model.App{Name: "tenant", Icon: "box", Color: "#112233"}, Config: json.RawMessage(`{}`),
		Files: []happ.ContentItemRequest{{Name: "compose.yml", Content: compose}},
	}}, nil)
}

// Backup health rides on the cached app status.
func TestAppsStatusCarriesBackupHealth(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	status, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{{AppID: anApp, StatusCode: command.ContainerStatusActive,
		Containers: []command.ContainerStatus{}, Backups: []command.BackupHealth{{Name: "database", State: command.BackupLate, LastSuccess: 1791720000}}}}})
	e.publishEvent(t, bus.EventMessage{ServerID: sid, Kind: bus.EventAppsStatus, Payload: status})
	eventually(t, func() bool {
		var got command.GetAppsStatusResponse
		owner.get(t, "/api/v1/app/get-apps-status", sq(sid)).expect(t, http.StatusOK).data(t, &got)
		return len(got.Apps) == 1 && len(got.Apps[0].Backups) == 1 && got.Apps[0].Backups[0].State == command.BackupLate
	})
}
