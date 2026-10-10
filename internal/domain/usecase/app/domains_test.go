package app

import (
	"context"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

// recordingDispatcher keeps every dispatch (nested ones included).
type recordingDispatcher struct{ all []port.DispatchInput }

func (r *recordingDispatcher) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	r.all = append(r.all, in)
	return "req-" + string(in.Type), nil
}

func (r *recordingDispatcher) last() port.DispatchInput { return r.all[len(r.all)-1] }

// fakeServerDomains is an in-memory port.ServerDomainRepository.
type fakeServerDomains struct {
	rows     map[string][]model.RegisteredDomain // serverID -> rows
	upserted []model.RegisteredDomain
	deleted  []string
}

func newFakeServerDomains(rows ...model.RegisteredDomain) *fakeServerDomains {
	f := &fakeServerDomains{rows: map[string][]model.RegisteredDomain{}}
	if len(rows) > 0 {
		f.rows["srv-1"] = rows
	}
	return f
}

func (f *fakeServerDomains) ListForServer(_ context.Context, serverID string) ([]model.RegisteredDomain, error) {
	return f.rows[serverID], nil
}
func (f *fakeServerDomains) Upsert(_ context.Context, serverID string, d model.RegisteredDomain) error {
	f.upserted = append(f.upserted, d)
	f.rows[serverID] = append(f.rows[serverID], d)
	return nil
}
func (f *fakeServerDomains) Delete(_ context.Context, _, name string) error {
	f.deleted = append(f.deleted, name)
	return nil
}
func (f *fakeServerDomains) ReplaceForServer(_ context.Context, serverID string, ds []model.RegisteredDomain) error {
	f.rows[serverID] = ds
	return nil
}

func newDomainsUseCase(d port.CommandDispatcher, sd port.ServerDomainRepository) *UseCase {
	return NewUseCase(&Deps{
		CommandDispatcher:      d,
		AppRepository:          noopAppRepo{},
		AppDomainRepository:    &fakeDomainRepo{},
		ServerDomainRepository: sd,
		Log:                    logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
	})
}

func TestListServerDomainsReconcilesTheIndex(t *testing.T) {
	disp := &recordingDispatcher{}
	sd := newFakeServerDomains(model.RegisteredDomain{Name: "stale.app", TLS: "auto"})
	uc := newDomainsUseCase(disp, sd)

	if _, err := uc.ListServerDomains(context.Background(), "u1", "srv-1"); err != nil {
		t.Fatal(err)
	}
	in := disp.last()
	if in.Type != command.TypeDomainList || in.AgentID != "srv-1" || in.UserID != "u1" {
		t.Fatalf("dispatch = %+v", in)
	}
	in.OnResult(port.CommandResult{Success: true, Payload: []byte(
		`{"domains":[{"name":"*.twillingate.app","tls":"certificate","not_after":"2027-01-02T03:04:05Z","issuer":"CF","apps":["a1"]}]}`)})
	got := sd.rows["srv-1"]
	if len(got) != 1 || got[0].Name != "*.twillingate.app" || got[0].TLS != "certificate" || got[0].Issuer != "CF" ||
		got[0].NotAfter == nil || !got[0].NotAfter.Equal(time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("index = %+v", got)
	}

	// A failed list leaves the index alone.
	in.OnResult(port.CommandResult{Success: false, Error: "agent offline"})
	in.OnResult(port.CommandResult{Success: true, Payload: []byte(`{not json`)})
	if len(sd.rows["srv-1"]) != 1 {
		t.Fatalf("failed results changed the index: %+v", sd.rows["srv-1"])
	}
}

func TestCreateAndDeleteDomainFollowTheAgent(t *testing.T) {
	disp := &recordingDispatcher{}
	sd := newFakeServerDomains()
	uc := newDomainsUseCase(disp, sd)
	ctx := context.Background()

	req := command.CreateDomainRequest{Name: "*.x.app", TLS: command.DomainTLSOff}
	if _, err := uc.CreateDomain(ctx, "u1", "srv-1", req); err != nil {
		t.Fatal(err)
	}
	create := disp.last()
	if create.Type != command.TypeDomainCreate || create.Payload != req {
		t.Fatalf("dispatch = %+v", create)
	}
	create.OnResult(port.CommandResult{Success: false, Error: "nope"})
	if len(sd.upserted) != 0 {
		t.Fatal("a refused create entered the index")
	}
	create.OnResult(port.CommandResult{Success: true, Payload: []byte(`{"domain":{"name":"*.x.app","tls":"off","apps":[]}}`)})
	if len(sd.upserted) != 1 || sd.upserted[0].Name != "*.x.app" || sd.upserted[0].TLS != "off" {
		t.Fatalf("upserted = %+v", sd.upserted)
	}

	if _, err := uc.DeleteDomain(ctx, "u1", "srv-1", "*.x.app"); err != nil {
		t.Fatal(err)
	}
	del := disp.last()
	if del.Type != command.TypeDomainDelete || del.Payload != (command.DeleteDomainRequest{Name: "*.x.app"}) {
		t.Fatalf("dispatch = %+v", del)
	}
	del.OnResult(port.CommandResult{Success: false, Error: "*.x.app is used by Shop (a1)"})
	if len(sd.deleted) != 0 {
		t.Fatal("a refused delete left the index")
	}
	del.OnResult(port.CommandResult{Success: true})
	if len(sd.deleted) != 1 || sd.deleted[0] != "*.x.app" {
		t.Fatalf("deleted = %v", sd.deleted)
	}
}

func TestRefreshAppsAlsoReconcilesDomains(t *testing.T) {
	disp := &recordingDispatcher{}
	uc := newDomainsUseCase(disp, newFakeServerDomains())
	if _, err := uc.RefreshApps(context.Background(), "u1", "srv-1"); err != nil {
		t.Fatal(err)
	}
	disp.last().OnResult(port.CommandResult{Success: true, Payload: []byte(`{"apps":[]}`)})
	if len(disp.all) != 2 || disp.all[1].Type != command.TypeDomainList || disp.all[1].AgentID != "srv-1" {
		t.Fatalf("dispatches = %+v", disp.all)
	}
}

func TestDomainResultsWithoutAnIndexAreNoOps(t *testing.T) {
	disp := &recordingDispatcher{}
	uc := newTestUseCase(disp) // no ServerDomainRepository
	ctx := context.Background()
	_, _ = uc.ListServerDomains(ctx, "u", "s")
	_, _ = uc.CreateDomain(ctx, "u", "s", command.CreateDomainRequest{Name: "x.app", TLS: command.DomainTLSOff})
	_, _ = uc.DeleteDomain(ctx, "u", "s", "x.app")
	for _, in := range disp.all {
		in.OnResult(port.CommandResult{Success: true, Payload: []byte(`{}`)})
	}
	_, _ = uc.RefreshApps(ctx, "u", "s")
	disp.last().OnResult(port.CommandResult{Success: true, Payload: []byte(`{"apps":[]}`)})
	if disp.last().Type != command.TypeAppsList {
		t.Fatal("refresh dispatched domain.list without an index to fill")
	}
}
