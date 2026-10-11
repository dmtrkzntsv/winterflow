package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/app/agent/backup"
	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/grpc/proto"
)

// fakeBackups records what the dispatcher asks of the scheduler.
type fakeBackups struct {
	mu       sync.Mutex
	reloads  int
	forgets  []string
	runs     []string
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
	resp = dispatchJSON(t, d, command.TypeAppDelete, command.DeleteAppRequest{AppID: "app-1"})
	if len(fb.forgets) != 2 || fb.forgets[0] != "app-1" {
		t.Fatalf("delete: forgets = %v (%+v)", fb.forgets, resp.Base)
	}

	// Invalid payloads never reach the orchestrator or the scheduler.
	for typ, req := range map[command.Type]any{
		command.TypeBackupUpdate:      command.UpdateBackupsRequest{AppID: "..", Backups: nil},
		command.TypeBackupPoints:      command.ListRestorePointsRequest{AppID: "app-1", Backup: "Bad"},
		command.TypeBackupRun:         command.RunBackupRequest{AppID: "a/b", Backup: "db"},
		command.TypeBackupDeletePoint: command.DeleteRestorePointRequest{AppID: "app-1", Backup: "db", Point: "../x"},
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
	var nb noBackups
	nb.Reload()
	nb.Forget("x")
	if apps := nb.Decorate([]command.AppStatus{{AppID: "a"}}, time.Now()); apps[0].Backups != nil {
		t.Fatal("decorated without a scheduler")
	}
}

type fakeStatus struct{ err error }

func (f fakeStatus) GetAppsStatus(context.Context) ([]command.AppStatus, error) {
	return []command.AppStatus{{AppID: "app-1"}}, f.err
}

type fakeCleaner struct{ err error }

func (f fakeCleaner) RemoveHelpers(context.Context) error { return f.err }

func TestWithBackupHealthAndRunBackups(t *testing.T) {
	fb := &fakeBackups{}
	apps, err := WithBackupHealth(fakeStatus{}, fb).GetAppsStatus(context.Background())
	if err != nil || apps[0].Backups[0].State != command.BackupOK {
		t.Fatalf("apps = %+v, %v", apps, err)
	}
	if _, err := WithBackupHealth(fakeStatus{err: errors.New("docker down")}, fb).GetAppsStatus(context.Background()); err == nil {
		t.Fatal("a failed collection passed")
	}
	// RunBackups cleans up, then runs the scheduler until ctx ends.
	d := newTestDispatcher(t)
	s := backup.New(d.orch, t.TempDir(), d.log)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { RunBackups(ctx, fakeCleaner{err: errors.New("docker down")}, s, d.log); close(done) }()
	o := <-s.RunNow("app-1", "db")
	if o.Err == nil || !strings.Contains(o.Err.Error(), "app app-1 not found") {
		t.Fatalf("outcome = %+v", o)
	}
	cancel()
	<-done
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
}
