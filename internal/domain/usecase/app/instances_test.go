package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

// fakeLinks is the template instances index: live maps "server/app" to the
// template name.
type fakeLinks struct {
	live    map[string]string
	retired []string
	err     error
}

func (f *fakeLinks) LiveInstanceForApp(_ context.Context, serverID, appID string) (string, bool, error) {
	name, ok := f.live[serverID+"/"+appID]
	return name, ok, f.err
}

func (f *fakeLinks) RetireForApp(_ context.Context, serverID, appID string) error {
	f.retired = append(f.retired, serverID+"/"+appID)
	delete(f.live, serverID+"/"+appID)
	return f.err
}

func newLinkedUseCase(d port.CommandDispatcher, links port.InstanceLinks) *UseCase {
	return NewUseCase(&Deps{CommandDispatcher: d, AppRepository: noopAppRepo{}, Instances: links,
		Log: logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})})
}

// save-app and rollback-app refuse an app a live instance manages; the
// instance routes (CheckSave and DispatchSave) and rename go on.
func TestInstanceAppsAreChangedThroughTheirInstance(t *testing.T) {
	disp := &recordingDispatcher{}
	links := &fakeLinks{live: map[string]string{"srv/a1": "shop"}}
	uc := newLinkedUseCase(disp, links)
	ctx := context.Background()
	payload := command.AppPayload{Config: []byte(`{}`)}

	_, err := uc.SaveApp(ctx, "u", "srv", model.App{ID: "a1", Name: "x"}, payload, false)
	if !errors.Is(err, model.ErrConflict) || !strings.Contains(err.Error(), "this app is managed by template shop: update the instance or unlink it") {
		t.Fatalf("save-app: %v", err)
	}
	if _, err := uc.RollbackApp(ctx, "u", "srv", "a1", "abc"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("rollback-app: %v", err)
	}
	if len(disp.all) != 0 {
		t.Fatalf("a refused call dispatched %d commands", len(disp.all))
	}
	if _, err := uc.SaveApp(ctx, "u", "srv", model.App{ID: "a2", Name: "x"}, payload, false); err != nil {
		t.Fatalf("an ordinary app: %v", err)
	}
	if _, err := uc.SaveApp(ctx, "u", "other", model.App{ID: "a1", Name: "x"}, payload, false); err != nil {
		t.Fatalf("the id on another server: %v", err)
	}
	if _, err := uc.SaveApp(ctx, "u", "srv", model.App{Name: "new"}, payload, false); err != nil {
		t.Fatalf("a new app: %v", err)
	}
	if _, err := uc.RenameApp(ctx, "u", "srv", "a1", "y"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	checked, err := uc.CheckSave(ctx, "srv", model.App{ID: "a1", Name: "x"}, payload, false)
	if err != nil {
		t.Fatalf("the instance's own check: %v", err)
	}
	if _, err := uc.DispatchSave(ctx, "u", checked, nil); err != nil {
		t.Fatalf("the instance's own save: %v", err)
	}

	links.live["srv/a1"] = ""
	if _, err := uc.SaveApp(ctx, "u", "srv", model.App{ID: "a1", Name: "x"}, payload, false); !errors.Is(err, model.ErrConflict) || !strings.Contains(err.Error(), "managed by a template") {
		t.Fatalf("a gone template's instance: %v", err)
	}
	links.err = errors.New("db down")
	if _, err := uc.SaveApp(ctx, "u", "srv", model.App{ID: "a3", Name: "x"}, payload, false); err == nil || errors.Is(err, model.ErrConflict) {
		t.Fatalf("a failed lookup: %v", err)
	}
}

// delete-app's success retires the app's instance key; a failure leaves it.
func TestDeleteAppRetiresTheInstance(t *testing.T) {
	disp := &recordingDispatcher{}
	links := &fakeLinks{live: map[string]string{"srv/a1": "shop"}}
	uc := newLinkedUseCase(disp, links)
	if _, err := uc.DeleteApp(context.Background(), "u", "srv", "a1", false, false); err != nil {
		t.Fatal(err)
	}
	disp.last().OnResult(port.CommandResult{Error: "boom"})
	if len(links.retired) != 0 {
		t.Fatalf("a failed delete retired %v", links.retired)
	}
	disp.last().OnResult(port.CommandResult{Success: true})
	if strings.Join(links.retired, ",") != "srv/a1" {
		t.Fatalf("retired = %v", links.retired)
	}
	// Without the index (no templates) delete-app works as before.
	plain := newTestUseCase(disp)
	if _, err := plain.DeleteApp(context.Background(), "u", "srv", "a1", false, false); err != nil {
		t.Fatal(err)
	}
	disp.last().OnResult(port.CommandResult{Success: true})
	if _, err := plain.SaveApp(context.Background(), "u", "srv", model.App{ID: "a1", Name: "x"}, command.AppPayload{Config: []byte(`{}`)}, false); err != nil {
		t.Fatal(err)
	}
}
