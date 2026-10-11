package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

const recipeCompose = `services:
  web:
    volumes: [data:/data]
  litestream:
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

func TestSaveAppChecksBackups(t *testing.T) {
	ss := newFakeServerStorages(model.StorageSummary{Name: "disk", Type: model.StorageLocal, Location: "/mnt/usb"})
	save := func(id, compose, config string) (*recordingDispatcher, error) {
		disp := &recordingDispatcher{}
		_, err := newStoragesUseCase(disp, ss).SaveApp(context.Background(), "u", "srv-1", model.App{ID: id, Name: "demo"}, command.AppPayload{
			Config: []byte(config), Files: []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
		}, false)
		return disp, err
	}
	for name, tc := range map[string]struct{ id, compose, config string }{
		"a recipe and its own references":  {"", recipeCompose, `{"name":"demo"}`},
		"an enabled backup on a storage":   {"", recipeCompose, `{"backups":[{"name":"replica","enabled":true,"storage":"disk"}]}`},
		"a UI-added backup referenced":     {"", recipeCompose + "# ${WF_BACKUP_AUDIT_PATH}\n", `{"backups":[{"name":"audit","type":"sqlite","volume":"data","path":"a.db"}]}`},
		"an edit without the backups list": {"app-1", recipeCompose + "# ${WF_BACKUP_AUDIT_PATH}\n", `{"name":"demo"}`},
	} {
		if disp, err := save(tc.id, tc.compose, tc.config); err != nil || len(disp.all) != 1 {
			t.Errorf("%s: err %v, dispatches %d", name, err, len(disp.all))
		}
	}
	for name, tc := range map[string]struct{ id, compose, config, want string }{
		"invalid recipe": {"", strings.Replace(recipeCompose, "volume: data", "volume: gone", 1), `{}`,
			`x-winterflow.backups[0].volume: "gone" is not a volume of this compose file`},
		"undeclared backup, new app": {"", recipeCompose + "# ${WF_BACKUP_AUDIT_PATH}\n", `{}`,
			`${WF_BACKUP_AUDIT_PATH}: there is no backup "audit" in this app`},
		"undeclared backup, with the list": {"app-1", recipeCompose + "# ${WF_BACKUP_AUDIT_PATH}\n", `{"backups":[]}`,
			`there is no backup "audit" in this app`},
		"storage not on the server": {"", recipeCompose, `{"backups":[{"name":"database","enabled":true,"storage":"nas"}]}`,
			`backup database: there is no storage "nas" on this server`},
		"bad backups list": {"", recipeCompose, `{"backups":{"name":"x"}}`, "backups: json: cannot unmarshal"},
		"invalid override": {"", recipeCompose, `{"backups":[{"name":"database","every":"1s"}]}`, "backups[0].every: 1s is too often"},
	} {
		disp, err := save(tc.id, tc.compose, tc.config)
		if !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
		if len(disp.all) != 0 {
			t.Errorf("%s: reached the agent", name)
		}
	}
}

// The API awaits a run longer than the agent holds it, so the agent's own
// timeout answer still finds its pending request.
func TestBackupRunWaitExceedsAgentBound(t *testing.T) {
	if backupRunWait < command.BackupRunWait+time.Minute {
		t.Fatalf("API wait %s does not exceed the agent's %s with slack", backupRunWait, command.BackupRunWait)
	}
}

func TestBackupCommands(t *testing.T) {
	ctx := context.Background()
	disp := &recordingDispatcher{}
	uc := newStoragesUseCase(disp, newFakeServerStorages(model.StorageSummary{Name: "disk", Type: model.StorageLocal, Location: "/mnt/usb"}))

	req := command.UpdateBackupsRequest{AppID: "a1", Backups: []recipe.BackupConfig{{Name: "db", Enabled: true, Storage: "disk"}, {Name: "x", Storage: "gone"}}}
	if id, err := uc.UpdateBackups(ctx, "u", "srv-1", req); err != nil || id != "req-backup.update" || disp.last().Payload.(command.UpdateBackupsRequest).AppID != "a1" {
		t.Fatalf("update = %q, %v", id, err)
	}
	req.Backups[1].Enabled = true
	if _, err := uc.UpdateBackups(ctx, "u", "srv-1", req); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), `backup x: there is no storage "gone"`) {
		t.Fatalf("update with a missing storage: %v", err)
	}
	if len(disp.all) != 1 {
		t.Fatal("a refused update reached the agent")
	}
	if _, err := uc.RestoreBackup(ctx, "u", "srv-1", command.RestoreBackupRequest{AppID: "a1", Backup: "db", Point: "20261011T120000Z-manual"}); err != nil ||
		disp.last().Wait != backupRunWait || disp.last().Type != command.TypeBackupRestore {
		t.Fatalf("restore = %+v, %v", disp.last(), err)
	}
	if _, err := uc.RunBackup(ctx, "u", "srv-1", command.RunBackupRequest{AppID: "a1", Backup: "db"}); err != nil || disp.last().Wait != backupRunWait || disp.last().Type != command.TypeBackupRun {
		t.Fatalf("run = %+v, %v", disp.last(), err)
	}
	if _, err := uc.ListRestorePoints(ctx, "u", "srv-1", command.ListRestorePointsRequest{AppID: "a1", Backup: "db"}); err != nil || disp.last().Type != command.TypeBackupPoints {
		t.Fatalf("points: %v", err)
	}
	if _, err := uc.DeleteRestorePoint(ctx, "u", "srv-1", command.DeleteRestorePointRequest{AppID: "a1", Backup: "db", Point: "p"}); err != nil || disp.last().Type != command.TypeBackupDeletePoint {
		t.Fatalf("delete point: %v", err)
	}
	// Without the index, the agent's check is the only one.
	bare := newStoragesUseCase(&recordingDispatcher{}, nil)
	if _, err := bare.UpdateBackups(ctx, "u", "srv-1", req); err != nil {
		t.Fatalf("no index: %v", err)
	}
}

// catalogRepo is an AppRepository that knows the apps in byID.
type catalogRepo struct {
	noopAppRepo
	byID map[string]model.App
	err  error
}

func (c catalogRepo) GetApp(_ context.Context, id string) (model.App, error) {
	if c.err != nil {
		return model.App{}, c.err
	}
	if a, ok := c.byID[id]; ok {
		return a, nil
	}
	return model.App{}, model.ErrAppNotFound
}

// An orphan's folder of an app that is live on another server is refused
// before it reaches the agent (which only knows its own server's apps); an
// unknown app, or one the catalog puts on this server, goes to the agent.
func TestDeleteOrphanRefusesAnAppLiveElsewhere(t *testing.T) {
	repo := catalogRepo{byID: map[string]model.App{
		"elsewhere": {ID: "elsewhere", ServerID: "srv-2", Name: "Shop"},
		"here":      {ID: "here", ServerID: "srv-1"},
	}}
	for app, wantRejected := range map[string]bool{"elsewhere": true, "here": false, "gone": false} {
		disp := &recordingDispatcher{}
		uc := NewUseCase(&Deps{CommandDispatcher: disp, AppRepository: repo})
		_, err := uc.DeleteOrphan(context.Background(), "u", "srv-1", command.DeleteOrphanRequest{Name: "disk", AppID: app})
		if wantRejected {
			if !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "live on another server") || len(disp.all) != 0 {
				t.Errorf("%s: err %v, dispatches %d", app, err, len(disp.all))
			}
			if strings.Contains(err.Error(), "Shop") {
				t.Errorf("the refusal names the other app: %v", err)
			}
			continue
		}
		if err != nil || len(disp.all) != 1 {
			t.Errorf("%s: err %v, dispatches %d", app, err, len(disp.all))
		}
	}
	// A failing catalog stops the request instead of letting it through.
	uc := NewUseCase(&Deps{CommandDispatcher: &recordingDispatcher{}, AppRepository: catalogRepo{err: errors.New("db down")}})
	if _, err := uc.DeleteOrphan(context.Background(), "u", "srv-1", command.DeleteOrphanRequest{Name: "disk", AppID: "x"}); err == nil {
		t.Error("a catalog error let the delete through")
	}
}
