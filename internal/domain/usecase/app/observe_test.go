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

// The …Observed variants the MCP tools use dispatch what their plain
// forms do, keep their result for ObservedResultTTL and run the observer
// after their own result handling; the plain forms set no wait.
// saveProbe is a save with nothing to check beyond its compose file.
var saveProbe = command.AppPayload{
	Config: []byte(`{"name":"web"}`),
	Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte("services:\n  web:\n    image: nginx\n")}},
}

func TestObservedVariants(t *testing.T) {
	disp := &recordingDispatcher{}
	uc := NewUseCase(&Deps{
		CommandDispatcher:      disp,
		AppRepository:          noopAppRepo{},
		ServerDomainRepository: newFakeIndex[model.RegisteredDomain](),
		Log:                    logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
	})
	ctx := context.Background()
	for _, tc := range []struct {
		typ      command.Type
		observed func(Observer) (string, error)
		plain    func() (string, error)
	}{
		{command.TypeAppsList,
			func(o Observer) (string, error) { return uc.RefreshAppsObserved(ctx, "u", "srv", o) },
			func() (string, error) { return uc.RefreshApps(ctx, "u", "srv") }},
		{command.TypeAppGet,
			func(o Observer) (string, error) { return uc.GetAppObserved(ctx, "u", "srv", "a1", o) },
			func() (string, error) { return uc.GetApp(ctx, "u", "srv", "a1") }},
		{command.TypeAppRevisions,
			func(o Observer) (string, error) { return uc.GetRevisionsObserved(ctx, "u", "srv", "a1", o) },
			func() (string, error) { return uc.GetRevisions(ctx, "u", "srv", "a1") }},
		{command.TypeAppRollback,
			func(o Observer) (string, error) { return uc.RollbackAppObserved(ctx, "u", "srv", "a1", "abc", o) },
			func() (string, error) { return uc.RollbackApp(ctx, "u", "srv", "a1", "abc") }},
		{command.TypeAppRename,
			func(o Observer) (string, error) { return uc.RenameAppObserved(ctx, "u", "srv", "a1", "n", o) },
			func() (string, error) { return uc.RenameApp(ctx, "u", "srv", "a1", "n") }},
		{command.TypeAppLogs,
			func(o Observer) (string, error) { return uc.GetLogsObserved(ctx, "u", "srv", "a1", 0, 100, o) },
			func() (string, error) { return uc.GetLogs(ctx, "u", "srv", "a1", 0, 100) }},
		{command.TypeImageTags,
			func(o Observer) (string, error) { return uc.GetImageTagsObserved(ctx, "u", "srv", "nginx", o) },
			func() (string, error) { return uc.GetImageTags(ctx, "u", "srv", "nginx") }},
		{command.TypeDomainList,
			func(o Observer) (string, error) { return uc.ListServerDomainsObserved(ctx, "u", "srv", o) },
			func() (string, error) { return uc.ListServerDomains(ctx, "u", "srv") }},
		{command.TypeDomainCreate,
			func(o Observer) (string, error) {
				return uc.CreateDomainObserved(ctx, "u", "srv", command.CreateDomainRequest{}, o)
			},
			func() (string, error) { return uc.CreateDomain(ctx, "u", "srv", command.CreateDomainRequest{}) }},
		{command.TypeDomainDelete,
			func(o Observer) (string, error) { return uc.DeleteDomainObserved(ctx, "u", "srv", "x.test", o) },
			func() (string, error) { return uc.DeleteDomain(ctx, "u", "srv", "x.test") }},
		{command.TypeAppSave,
			func(o Observer) (string, error) {
				return uc.SaveAppObserved(ctx, "u", "srv", model.App{Name: "web"}, saveProbe, false, o)
			},
			func() (string, error) { return uc.SaveApp(ctx, "u", "srv", model.App{Name: "web"}, saveProbe, false) }},
	} {
		seen := 0
		id, err := tc.observed(func(port.CommandResult) { seen++ })
		in := disp.last()
		if err != nil || id != "req-"+string(tc.typ) || in.Type != tc.typ || in.Wait != ObservedResultTTL || in.OnResult == nil {
			t.Errorf("%s observed: id %q, type %s, wait %v, %v", tc.typ, id, in.Type, in.Wait, err)
			continue
		}
		in.OnResult(port.CommandResult{Error: "boom"})
		if seen != 1 {
			t.Errorf("%s: observer ran %d times", tc.typ, seen)
		}
		if _, err := tc.plain(); err != nil || disp.last().Type != tc.typ || disp.last().Wait != 0 {
			t.Errorf("%s plain: type %s, wait %v, %v", tc.typ, disp.last().Type, disp.last().Wait, err)
		}
	}
}
