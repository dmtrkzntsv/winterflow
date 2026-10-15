package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// A command with an observer keeps its result past the dispatcher's default
// (an instance call may be waited on for the handler's bound); one without
// (the web UI's) is left at the dispatcher's default.
func TestObservedDispatchesKeepTheirResult(t *testing.T) {
	disp := &recordingDispatcher{}
	uc := newTestUseCase(disp)
	ctx := context.Background()
	observe := func(port.CommandResult) {}
	checked, err := uc.CheckSave(ctx, "srv", model.App{ID: "a1", Name: "x"}, command.AppPayload{Config: []byte(`{}`)}, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, dispatch := range map[string]func(Observer) error{
		"save": func(o Observer) error { _, err := uc.DispatchSave(ctx, "u", checked, o); return err },
		"control": func(o Observer) error {
			_, err := uc.ControlAppObserved(ctx, "u", "srv", "a1", command.ControlStop, o)
			return err
		},
		"delete": func(o Observer) error {
			_, err := uc.DeleteAppObserved(ctx, "u", "srv", "a1", false, false, o)
			return err
		},
	} {
		if err := dispatch(observe); err != nil || disp.last().Wait != ObservedResultTTL {
			t.Errorf("%s with an observer: wait %v, %v", name, disp.last().Wait, err)
		}
		if err := dispatch(nil); err != nil || disp.last().Wait != 0 {
			t.Errorf("%s without one: wait %v, %v", name, disp.last().Wait, err)
		}
	}
	if ObservedResultTTL != 15*time.Minute {
		t.Errorf("ObservedResultTTL = %v", ObservedResultTTL)
	}
}
