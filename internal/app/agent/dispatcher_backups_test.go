package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/app/agent/backup"
	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/internal/infra/transport/grpc/proto"
)

// fakeBackups records what the dispatcher asks of the scheduler.
type fakeBackups struct {
	mu       sync.Mutex
	reloads  int
	forgets  []string
	runs     []string
	restores []string
	outcome  backup.Outcome
	hold     chan struct{} // RunNow answers only once closed
	decorate int
}

func (f *fakeBackups) Reload() { f.mu.Lock(); f.reloads++; f.mu.Unlock() }
func (f *fakeBackups) Forget(appID string) {
	f.mu.Lock()
	f.forgets = append(f.forgets, appID)
	f.mu.Unlock()
}
func (f *fakeBackups) RunNow(appID, name string) <-chan backup.Outcome {
	f.mu.Lock()
	f.runs = append(f.runs, appID+"/"+name)
	o, hold := f.outcome, f.hold
	f.mu.Unlock()
	ch := make(chan backup.Outcome, 1)
	go func() {
		if hold != nil {
			<-hold
		}
		ch <- o
	}()
	return ch
}
func (f *fakeBackups) Restore(appID, name, point string) <-chan backup.Outcome {
	f.mu.Lock()
	f.restores = append(f.restores, appID+"/"+name+"@"+point)
	o := f.outcome
	f.mu.Unlock()
	ch := make(chan backup.Outcome, 1)
	ch <- o
	return ch
}
func (f *fakeBackups) Decorate(apps []command.AppStatus, _ time.Time) []command.AppStatus {
	f.mu.Lock()
	f.decorate++
	f.mu.Unlock()
	for i := range apps {
		apps[i].Backups = []command.BackupHealth{{Name: "db", State: command.BackupOK}}
	}
	return apps
}

func (f *fakeBackups) reloadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reloads
}

const backupsCompose = `services:
  web:
    image: example/web
    volumes: [data:/data]
  litestream:
    image: litestream/litestream
    profiles: [wf-backup-replica]
volumes:
  data:
x-winterflow:
  backups:
    - name: database
      type: sqlite
      volume: data
      path: app.db
    - name: replica
      type: custom
      service: litestream
`

func TestDispatchBackupCommands(t *testing.T) {
	d := newTestDispatcher(t)
	fb := &fakeBackups{outcome: backup.Outcome{Result: command.RunBackupResponse{AppID: "app-1", Backup: "database", Unchanged: true}}}
	d.SetBackups(fb)
	ok := proto.ResponseCode_RESPONSE_CODE_SUCCESS
	if resp := dispatchJSON(t, d, command.TypeStorageSave, command.SaveStorageRequest{Create: true,
		Storage: model.Storage{Name: "disk", Type: model.StorageLocal, Local: &model.LocalStorage{Path: t.TempDir()}}}); resp.Base.ResponseCode != ok {
		t.Fatalf("storage.save = %+v", resp.Base)
	}
	resp := dispatchJSON(t, d, command.TypeAppSave, command.SaveAppRequest{Draft: true, App: command.AppPayload{AppID: "app-1",
		Config: []byte(`{"name":"demo","files":[{"filename":"compose.yml"}],"backups":[{"name":"database","storage":"disk"}]}`),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte(backupsCompose)}}}})
	if resp.Base.ResponseCode != ok {
		t.Fatalf("app.save = %+v", resp.Base)
	}
	reloads := fb.reloadCount()
	if reloads != 2 {
		t.Fatalf("reloads after storage.save and app.save = %d", reloads)
	}

	resp = dispatchJSON(t, d, command.TypeBackupUpdate, command.UpdateBackupsRequest{AppID: "app-1",
		Backups: []recipe.BackupConfig{{Name: "database", Storage: "disk"}, {Name: "replica", Enabled: true, Storage: "disk"}}})
	var updated command.UpdateBackupsResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &updated) != nil || updated.Revision == "" || !updated.Backups[1].Enabled {
		t.Fatalf("backup.update = %+v %s", resp.Base, resp.Payload)
	}
	if fb.reloadCount() != reloads+1 {
		t.Fatal("backup.update did not reload the scheduler")
	}
	resp = dispatchJSON(t, d, command.TypeBackupUpdate, command.UpdateBackupsRequest{AppID: "app-1",
		Backups: []recipe.BackupConfig{{Name: "replica", Enabled: true, Storage: "nas"}}})
	if resp.Base.ResponseCode == ok || !strings.Contains(resp.Base.Detail, `there is no storage "nas"`) {
		t.Fatalf("refused update = %+v", resp.Base)
	}

	resp = dispatchJSON(t, d, command.TypeBackupPoints, command.ListRestorePointsRequest{AppID: "app-1", Backup: "database"})
	var listed command.ListRestorePointsResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &listed) != nil || listed.Points == nil || len(listed.Points) != 0 {
		t.Fatalf("backup.points = %+v %s", resp.Base, resp.Payload)
	}
	resp = dispatchJSON(t, d, command.TypeBackupDeletePoint, command.DeleteRestorePointRequest{AppID: "app-1", Backup: "database", Point: "20261011T120000Z-manual"})
	if resp.Base.ResponseCode == ok || !strings.Contains(resp.Base.Detail, "has no restore point") {
		t.Fatalf("backup.delete-point = %+v", resp.Base)
	}

	resp = dispatchJSON(t, d, command.TypeBackupRun, command.RunBackupRequest{AppID: "app-1", Backup: "database"})
	var run command.RunBackupResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &run) != nil || !run.Unchanged || fb.runs[0] != "app-1/database" {
		t.Fatalf("backup.run = %+v %s", resp.Base, resp.Payload)
	}
	fb.outcome = backup.Outcome{Err: errors.New("database file not found")}
	if resp = dispatchJSON(t, d, command.TypeBackupRun, command.RunBackupRequest{AppID: "app-1", Backup: "database"}); resp.Base.Detail != "database file not found" {
		t.Fatalf("failed run = %+v", resp.Base)
	}
	// A restore waits for its outcome on the scheduler.
	fb.outcome = backup.Outcome{Restore: command.RestoreBackupResponse{AppID: "app-1", Backup: "database",
		RestoredPoint: "20261011T120000Z-manual", PreRestorePoint: "20261011T130000Z-pre-restore"}}
	resp = dispatchJSON(t, d, command.TypeBackupRestore, command.RestoreBackupRequest{AppID: "app-1", Backup: "database", Point: "20261011T120000Z-manual"})
	var restoredResp command.RestoreBackupResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &restoredResp) != nil ||
		restoredResp.PreRestorePoint != "20261011T130000Z-pre-restore" || fb.restores[0] != "app-1/database@20261011T120000Z-manual" {
		t.Fatalf("backup.restore = %+v %s", resp.Base, resp.Payload)
	}
	fb.outcome = backup.Outcome{Err: errors.New("the restore failed, the app's data is unchanged")}
	if resp = dispatchJSON(t, d, command.TypeBackupRestore, command.RestoreBackupRequest{AppID: "app-1", Backup: "database",
		Point: "20261011T120000Z-manual"}); resp.Base.Detail != "the restore failed, the app's data is unchanged" {
		t.Fatalf("failed restore = %+v", resp.Base)
	}

	// Orphans: none in the storage while the app is here; its folder is
	// refused.
	resp = dispatchJSON(t, d, command.TypeStorageOrphans, command.ListOrphansRequest{Name: "disk"})
	var orphans command.ListOrphansResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &orphans) != nil || orphans.Orphans == nil || len(orphans.Orphans) != 0 {
		t.Fatalf("storage.orphans = %+v %s", resp.Base, resp.Payload)
	}
	resp = dispatchJSON(t, d, command.TypeStorageDeleteOrphan, command.DeleteOrphanRequest{Name: "disk", AppID: "app-1"})
	if resp.Base.ResponseCode == ok || !strings.Contains(resp.Base.Detail, "is on this server") {
		t.Fatalf("storage.delete-orphan = %+v", resp.Base)
	}
	resp = dispatchJSON(t, d, command.TypeStorageDeleteOrphan, command.DeleteOrphanRequest{Name: "disk", AppID: "gone"})
	var deleted command.DeleteOrphanResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &deleted) != nil || deleted.AppID != "gone" || deleted.Deleted != 0 {
		t.Fatalf("storage.delete-orphan gone = %+v %s", resp.Base, resp.Payload)
	}

	// A request whose caller gave up stops waiting.
	fb.hold = make(chan struct{})
	defer close(fb.hold)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	payload, _ := json.Marshal(command.RunBackupRequest{AppID: "app-1", Backup: "database"})
	if resp := d.Dispatch(ctx, envelope(string(command.TypeBackupRun), payload)); resp.Base.ResponseCode == ok {
		t.Fatalf("canceled run = %+v", resp.Base)
	}

	resp = dispatchJSON(t, d, command.TypeAppsStatus, command.GetAppsStatusRequest{})
	var status command.GetAppsStatusResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &status) != nil || len(status.Apps) != 1 || status.Apps[0].Backups[0].Name != "db" {
		t.Fatalf("apps.status = %+v %s", resp.Base, resp.Payload)
	}

	if resp = dispatchJSON(t, d, command.TypeAppRename, command.RenameAppRequest{AppID: "app-1", Name: "renamed"}); resp.Base.ResponseCode != ok {
		t.Fatalf("rename = %+v", resp.Base)
	}
	resp = dispatchJSON(t, d, command.TypeAppDelete, command.DeleteAppRequest{AppID: "app-1", DeleteBackups: true})
	var deletedApp command.DeleteAppResponse
	if len(fb.forgets) != 2 || fb.forgets[0] != "app-1" || json.Unmarshal(resp.Payload, &deletedApp) != nil || !deletedApp.BackupsDeleted {
		t.Fatalf("delete: forgets = %v (%+v %s)", fb.forgets, resp.Base, resp.Payload)
	}

	// Invalid payloads never reach the orchestrator or the scheduler.
	for typ, req := range map[command.Type]any{
		command.TypeBackupUpdate:        command.UpdateBackupsRequest{AppID: "..", Backups: nil},
		command.TypeBackupPoints:        command.ListRestorePointsRequest{AppID: "app-1", Backup: "Bad"},
		command.TypeBackupRun:           command.RunBackupRequest{AppID: "a/b", Backup: "db"},
		command.TypeBackupDeletePoint:   command.DeleteRestorePointRequest{AppID: "app-1", Backup: "db", Point: "../x"},
		command.TypeBackupRestore:       command.RestoreBackupRequest{AppID: "app-1", Backup: "db", Point: "latest"},
		command.TypeStorageOrphans:      command.ListOrphansRequest{Name: "Disk"},
		command.TypeStorageDeleteOrphan: command.DeleteOrphanRequest{Name: "disk", AppID: "../x"},
	} {
		if resp := dispatchJSON(t, d, typ, req); resp.Base.ResponseCode == ok || !strings.HasPrefix(resp.Base.Detail, "invalid payload: ") {
			t.Errorf("%s: %+v", typ, resp.Base)
		}
	}
}

// Without a scheduler, Back up now fails cleanly and statuses pass through.
func TestDispatchWithoutBackups(t *testing.T) {
	d := newTestDispatcher(t)
	resp := dispatchJSON(t, d, command.TypeBackupRun, command.RunBackupRequest{AppID: "app-1", Backup: "db"})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || resp.Base.Detail != errNoBackups.Error() {
		t.Fatalf("backup.run = %+v", resp.Base)
	}
	resp = dispatchJSON(t, d, command.TypeBackupRestore, command.RestoreBackupRequest{AppID: "app-1", Backup: "db", Point: "20261011T120000Z-manual"})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || resp.Base.Detail != errNoBackups.Error() {
		t.Fatalf("backup.restore = %+v", resp.Base)
	}
	var nb noBackups
	nb.Reload()
	nb.Forget("x")
	if apps := nb.Decorate([]command.AppStatus{{AppID: "a"}}, time.Now()); apps[0].Backups != nil {
		t.Fatal("decorated without a scheduler")
	}
}

type fakeCleaner struct {
	err         error
	interrupted []dockercompose.RestoreMark
}

func (f fakeCleaner) RemoveHelpers(context.Context) error { return f.err }
func (f fakeCleaner) InterruptedRestores() []dockercompose.RestoreMark {
	return f.interrupted
}

func TestWithBackupHealthAndRunBackups(t *testing.T) {
	fb := &fakeBackups{}
	resp, err := WithBackupHealth(&fakeStatus{apps: []command.AppStatus{{AppID: "app-1"}}}, fb).StatusReport(context.Background())
	apps := resp.Apps
	if err != nil || apps[0].Backups[0].State != command.BackupOK {
		t.Fatalf("apps = %+v, %v", apps, err)
	}
	if _, err := WithBackupHealth(&fakeStatus{err: errors.New("docker down")}, fb).StatusReport(context.Background()); err == nil {
		t.Fatal("a failed collection passed")
	}
	// RunBackups cleans up, then runs the scheduler until ctx ends.
	d := newTestDispatcher(t)
	stateDir := t.TempDir()
	s := backup.New(d.orch, stateDir, d.log)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	cut := dockercompose.RestoreMark{App: "app-1", Backup: "db", Point: "20261011T120000Z-manual", PreRestorePoint: "pre", Phase: "restoring"}
	go func() {
		RunBackups(ctx, fakeCleaner{err: errors.New("docker down"), interrupted: []dockercompose.RestoreMark{cut}}, s, d.log)
		close(done)
	}()
	o := <-s.RunNow("app-1", "db")
	if o.Err == nil || !strings.Contains(o.Err.Error(), "app app-1 not found") {
		t.Fatalf("outcome = %+v", o)
	}
	cancel()
	<-done
	// The restore the last run was cut short in is the backup's last_error
	// (state.json), whether or not the app still exists.
	raw, err := os.ReadFile(filepath.Join(stateDir, "backups", "state.json"))
	if err != nil || !strings.Contains(string(raw), cut.Message()) {
		t.Fatalf("state.json = %s (%v)", raw, err)
	}
}

// A Back up now request is bounded: a scheduler that never answers (here one
// that was never started) fails it at the limit, and one that has stopped
// fails it at once.
func TestRunBackupIsBounded(t *testing.T) {
	d := newTestDispatcher(t)
	s := backup.New(d.orch, t.TempDir(), d.log)
	d.SetBackups(s)
	d.runWait = 50 * time.Millisecond
	start := time.Now()
	resp := dispatchJSON(t, d, command.TypeBackupRun, command.RunBackupRequest{AppID: "app-1", Backup: "db"})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || !strings.Contains(resp.Base.Detail, "deadline exceeded") {
		t.Fatalf("unanswered run = %+v", resp.Base)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the request outlived its limit")
	}

	d.runWait = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Run(ctx) // returns at once: the scheduler is stopped
	start = time.Now()
	resp = dispatchJSON(t, d, command.TypeBackupRun, command.RunBackupRequest{AppID: "app-1", Backup: "db"})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || !strings.Contains(resp.Base.Detail, "stopping") {
		t.Fatalf("run on a stopped scheduler = %+v", resp.Base)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("a stopped scheduler left the request waiting")
	}
	resp = dispatchJSON(t, d, command.TypeBackupRestore, command.RestoreBackupRequest{AppID: "app-1", Backup: "db", Point: "20261011T120000Z-manual"})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || !strings.Contains(resp.Base.Detail, "stopping") {
		t.Fatalf("restore on a stopped scheduler = %+v", resp.Base)
	}
}

// A restore request is bounded like a run: a scheduler that never answers
// fails it at the limit.
func TestRestoreBackupIsBounded(t *testing.T) {
	d := newTestDispatcher(t)
	d.SetBackups(backup.New(d.orch, t.TempDir(), d.log))
	d.runWait = 50 * time.Millisecond
	resp := dispatchJSON(t, d, command.TypeBackupRestore, command.RestoreBackupRequest{AppID: "app-1", Backup: "db", Point: "20261011T120000Z-manual"})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || !strings.Contains(resp.Base.Detail, "deadline exceeded") {
		t.Fatalf("unanswered restore = %+v", resp.Base)
	}
}
