package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

// recordingAppRepo records the catalog writes, to order them against an
// observer.
type recordingAppRepo struct {
	noopAppRepo
	events *[]string
}

func (r recordingAppRepo) SaveApp(_ context.Context, a model.App) error {
	*r.events = append(*r.events, "persist "+a.ID+" template="+a.TemplateID)
	return nil
}

func (r recordingAppRepo) DeleteApp(_ context.Context, id string) error {
	*r.events = append(*r.events, "delete "+id)
	return nil
}

func TestCheckSaveDispatchesNothing(t *testing.T) {
	disp := &recordingDispatcher{}
	uc := newTestUseCase(disp)
	checked, err := uc.CheckSave(context.Background(), "srv", model.App{Name: "x"}, command.AppPayload{Config: []byte(`{}`)}, false)
	if err != nil || checked.AppID() == "" || len(disp.all) != 0 {
		t.Fatalf("check = %+v, %v, %d dispatches", checked, err, len(disp.all))
	}
	_, err = uc.CheckSave(context.Background(), "srv", model.App{Name: "x"}, command.AppPayload{
		Config: []byte(`{}`), Variables: []command.ContentItem{{Name: "WF_STORAGE_X_PATH"}},
	}, false)
	if !errors.Is(err, model.ErrRejected) {
		t.Fatalf("reserved variable: %v", err)
	}
}

// The observer runs after the usecase's own result handling, on success
// and on failure.
func TestObserversRunAfterTheDB(t *testing.T) {
	var events []string
	disp := &recordingDispatcher{}
	uc := NewUseCase(&Deps{CommandDispatcher: disp, AppRepository: recordingAppRepo{events: &events}, Log: logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})})
	observe := func(res port.CommandResult) {
		events = append(events, "observe "+map[bool]string{true: "ok", false: "fail"}[res.Success])
	}
	checked, _ := uc.CheckSave(context.Background(), "srv", model.App{ID: "a1", Name: "x", TemplateID: "t1"}, command.AppPayload{Config: []byte(`{}`)}, false)
	if _, err := uc.DispatchSave(context.Background(), "u", checked, observe); err != nil {
		t.Fatal(err)
	}
	disp.last().OnResult(port.CommandResult{Success: true, Payload: []byte(`{"app_id":"a1"}`)})
	disp.last().OnResult(port.CommandResult{Error: "boom"})
	if _, err := uc.ControlAppObserved(context.Background(), "u", "srv", "a1", command.ControlStop, observe); err != nil {
		t.Fatal(err)
	}
	disp.last().OnResult(port.CommandResult{Success: true})
	if _, err := uc.DeleteAppObserved(context.Background(), "u", "srv", "a1", false, false, observe); err != nil {
		t.Fatal(err)
	}
	disp.last().OnResult(port.CommandResult{Success: true})
	want := "persist a1 template=t1,observe ok,observe fail,observe ok,delete a1,observe ok"
	if got := strings.Join(events, ","); got != want {
		t.Fatalf("events = %s\nwant     %s", got, want)
	}
	// Without an observer control sets no hook at all.
	if _, err := uc.ControlApp(context.Background(), "u", "srv", "a1", command.ControlStart); err != nil || disp.last().OnResult != nil {
		t.Fatalf("plain control: %v", err)
	}
}

// An upgrade's backup defaults need their storage on the server like a
// backups list does.
func TestSaveChecksInstanceBackupDefaults(t *testing.T) {
	ss := newFakeServerStorages(model.StorageSummary{Name: "disk", Type: model.StorageLocal, Location: "/mnt"})
	uc := newStoragesUseCase(&recordingDispatcher{}, ss)
	compose := "services: {app: {image: x, volumes: [data:/data]}}\nvolumes: {data: {}}\nx-winterflow:\n  backups:\n" +
		"    - {name: db, type: sqlite, volume: data, path: a.db}\n    - {name: media, type: files, volume: data}\n"
	save := func(defaults []recipe.BackupConfig) error {
		cfg := `{"instance":{"template_id":"t","template_name":"s","revision":2,"key":"k","backup_defaults":` + mustJSON(t, defaults) + `}}`
		_, err := uc.CheckSave(context.Background(), "srv-1", model.App{ID: "a1", Name: "x"}, command.AppPayload{
			Config: []byte(cfg), Files: []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
		}, false)
		return err
	}
	if err := save([]recipe.BackupConfig{{Name: "media", Enabled: true, Storage: "disk"}, {Name: "db", Storage: "gone"}}); err != nil {
		t.Fatalf("a known storage (and a disabled default's): %v", err)
	}
	if err := save([]recipe.BackupConfig{{Name: "media", Enabled: true, Storage: "gone"}}); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), `no storage "gone"`) {
		t.Fatalf("an unknown storage: %v", err)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
