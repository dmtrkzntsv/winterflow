package app

import (
	"context"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

type captureDispatcher struct {
	last port.DispatchInput
}

func (c *captureDispatcher) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	c.last = in
	return "req-1", nil
}

type noopAppRepo struct{ port.AppRepository }

func newTestUseCase(d port.CommandDispatcher) *UseCase {
	return NewUseCase(&Deps{
		CommandDispatcher: d,
		AppRepository:     noopAppRepo{},
		Log:               logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
	})
}

func TestCreateAppAssignsIDWhenMissing(t *testing.T) {
	disp := &captureDispatcher{}
	uc := newTestUseCase(disp)

	reqID, err := uc.CreateApp(context.Background(), "user-1", "server-1", model.App{Name: "demo"}, command.AppPayload{
		Config: []byte(`{"name":"demo"}`),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte("services: {}")}},
	}, false)
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if reqID != "req-1" {
		t.Errorf("requestID = %q, want req-1", reqID)
	}

	payload, ok := disp.last.Payload.(command.SaveAppRequest)
	if !ok {
		t.Fatalf("payload type = %T, want SaveAppRequest", disp.last.Payload)
	}
	if payload.App.AppID == "" {
		t.Error("app_id was not assigned for a new app (would cause the agent's 'app_id is required' error)")
	}
}

func TestCreateAppKeepsProvidedID(t *testing.T) {
	disp := &captureDispatcher{}
	uc := newTestUseCase(disp)

	_, err := uc.CreateApp(context.Background(), "u", "s", model.App{ID: "fixed-id", Name: "demo"}, command.AppPayload{
		Config: []byte(`{}`),
	}, false)
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	payload := disp.last.Payload.(command.SaveAppRequest)
	if payload.App.AppID != "fixed-id" {
		t.Errorf("app_id = %q, want fixed-id", payload.App.AppID)
	}
}
