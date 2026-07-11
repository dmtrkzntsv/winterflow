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

type captureDispatcher struct {
	last port.DispatchInput
}

func (c *captureDispatcher) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	c.last = in
	return "req-1", nil
}

// noopAppRepo is a no-op port.AppRepository: it satisfies every method with an
// explicit stub (rather than embedding a nil interface) so tests can safely
// invoke OnResult callbacks that call into the repo.
type noopAppRepo struct{}

func (noopAppRepo) GetApps(_ context.Context, _ string) ([]model.App, error) { return nil, nil }
func (noopAppRepo) GetApp(_ context.Context, _ string) (model.App, error)    { return model.App{}, nil }
func (noopAppRepo) SaveApp(_ context.Context, _ model.App) error             { return nil }
func (noopAppRepo) DeleteApp(_ context.Context, _ string) error              { return nil }
func (noopAppRepo) RenameApp(_ context.Context, _, _ string) error           { return nil }
func (noopAppRepo) SyncApps(_ context.Context, _ string, _ []model.App) error {
	return nil
}

func newTestUseCase(d port.CommandDispatcher) *UseCase {
	return NewUseCase(&Deps{
		CommandDispatcher: d,
		AppRepository:     noopAppRepo{},
		Log:               logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
	})
}

func TestSaveAppAssignsIDWhenMissing(t *testing.T) {
	disp := &captureDispatcher{}
	uc := newTestUseCase(disp)

	reqID, err := uc.SaveApp(context.Background(), "user-1", "server-1", model.App{Name: "demo"}, command.AppPayload{
		Config: []byte(`{"name":"demo"}`),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte("services: {}")}},
	}, false)
	if err != nil {
		t.Fatalf("SaveApp: %v", err)
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

func TestSaveAppKeepsProvidedID(t *testing.T) {
	disp := &captureDispatcher{}
	uc := newTestUseCase(disp)

	_, err := uc.SaveApp(context.Background(), "u", "s", model.App{ID: "fixed-id", Name: "demo"}, command.AppPayload{
		Config: []byte(`{}`),
	}, false)
	if err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	payload := disp.last.Payload.(command.SaveAppRequest)
	if payload.App.AppID != "fixed-id" {
		t.Errorf("app_id = %q, want fixed-id", payload.App.AppID)
	}
}

type fakeDomainRepo struct {
	claims   []model.DomainClaim
	replaced struct {
		appID    string
		serverID string
		ing      *model.Ingress
	}
	deleted string
	synced  string
}

func (f *fakeDomainRepo) FindClaims(_ context.Context, _ []string, _ string) ([]model.DomainClaim, error) {
	return f.claims, nil
}
func (f *fakeDomainRepo) ReplaceForApp(_ context.Context, appID, serverID string, ing *model.Ingress) error {
	f.replaced.appID, f.replaced.serverID, f.replaced.ing = appID, serverID, ing
	return nil
}
func (f *fakeDomainRepo) DeleteForApp(_ context.Context, appID string) error {
	f.deleted = appID
	return nil
}
func (f *fakeDomainRepo) ReplaceForServer(_ context.Context, serverID string, _ []model.App) error {
	f.synced = serverID
	return nil
}
func (f *fakeDomainRepo) ListForServer(_ context.Context, _ string) (map[string][]model.AppDomainInfo, error) {
	return nil, nil
}

const ingressConfig = `{"name":"demo","ingress":{"domains":[{"domain":"blog.example.com","upstream_port":8088,"ssl":true}],"redirects":[]}}`

func newTestUseCaseWithDomains(d port.CommandDispatcher, domains port.AppDomainRepository) *UseCase {
	return NewUseCase(&Deps{
		CommandDispatcher:   d,
		AppRepository:       noopAppRepo{},
		AppDomainRepository: domains,
		Log:                 logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
	})
}

func TestSaveAppRejectsInvalidIngress(t *testing.T) {
	disp := &captureDispatcher{}
	uc := newTestUseCaseWithDomains(disp, &fakeDomainRepo{})

	bad := `{"name":"demo","ingress":{"domains":[{"domain":"NOT A HOST","upstream_port":8088}],"redirects":[]}}`
	_, err := uc.SaveApp(context.Background(), "u", "s", model.App{Name: "demo"}, command.AppPayload{Config: []byte(bad)}, false)
	if !errors.Is(err, model.ErrIngressInvalid) {
		t.Fatalf("err = %v, want ErrIngressInvalid", err)
	}
	if disp.last.Type != "" {
		t.Fatal("invalid ingress reached the bus")
	}
}

func TestSaveAppRejectsTakenDomain(t *testing.T) {
	disp := &captureDispatcher{}
	repo := &fakeDomainRepo{claims: []model.DomainClaim{{Domain: "blog.example.com", AppName: "Ghost", ServerName: "hetzner-1"}}}
	uc := newTestUseCaseWithDomains(disp, repo)

	_, err := uc.SaveApp(context.Background(), "u", "s", model.App{Name: "demo"}, command.AppPayload{Config: []byte(ingressConfig)}, false)
	if !errors.Is(err, model.ErrDomainTaken) {
		t.Fatalf("err = %v, want ErrDomainTaken", err)
	}
	for _, part := range []string{"blog.example.com", "Ghost", "hetzner-1"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("error %q missing %q", err, part)
		}
	}
}

func TestSaveAppReplacesIndexOnSuccess(t *testing.T) {
	disp := &captureDispatcher{}
	repo := &fakeDomainRepo{}
	uc := newTestUseCaseWithDomains(disp, repo)

	_, err := uc.SaveApp(context.Background(), "u", "srv-9", model.App{ID: "app-7", Name: "demo"}, command.AppPayload{Config: []byte(ingressConfig)}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the agent confirming the save.
	disp.last.OnResult(port.CommandResult{Success: true, Payload: []byte(`{"app_id":"app-7","revision":"abc"}`)})
	if repo.replaced.appID != "app-7" || repo.replaced.serverID != "srv-9" || repo.replaced.ing == nil {
		t.Fatalf("ReplaceForApp not called correctly: %+v", repo.replaced)
	}
}

func TestDeleteAppRemovesDomainRows(t *testing.T) {
	disp := &captureDispatcher{}
	repo := &fakeDomainRepo{}
	uc := newTestUseCaseWithDomains(disp, repo)

	_, err := uc.DeleteApp(context.Background(), "u", "s", "app-7")
	if err != nil {
		t.Fatal(err)
	}
	disp.last.OnResult(port.CommandResult{Success: true})
	if repo.deleted != "app-7" {
		t.Fatalf("DeleteForApp = %q, want app-7", repo.deleted)
	}
}

func TestRefreshAppsSyncsDomains(t *testing.T) {
	disp := &captureDispatcher{}
	repo := &fakeDomainRepo{}
	uc := newTestUseCaseWithDomains(disp, repo)

	_, err := uc.RefreshApps(context.Background(), "u", "srv-9")
	if err != nil {
		t.Fatal(err)
	}
	disp.last.OnResult(port.CommandResult{Success: true, Payload: []byte(`{"apps":[{"id":"a1","name":"x","ingress":{"domains":[{"domain":"a.example.com","upstream_port":81}],"redirects":[]}}]}`)})
	if repo.synced != "srv-9" {
		t.Fatalf("ReplaceForServer not called (synced=%q)", repo.synced)
	}
}

func TestCheckDomain(t *testing.T) {
	repo := &fakeDomainRepo{claims: []model.DomainClaim{{Domain: "x.example.com", AppName: "Other"}}}
	uc := newTestUseCaseWithDomains(&captureDispatcher{}, repo)

	claims, err := uc.CheckDomain(context.Background(), "x.example.com", "app-1")
	if err != nil || len(claims) != 1 {
		t.Fatalf("CheckDomain = %v, %v", claims, err)
	}

	if _, err := uc.CheckDomain(context.Background(), "NOT A HOST", ""); !errors.Is(err, model.ErrIngressInvalid) {
		t.Fatalf("err = %v, want ErrIngressInvalid", err)
	}
}
