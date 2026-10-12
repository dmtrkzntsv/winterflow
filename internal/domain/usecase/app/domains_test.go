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

// fakeIndex is an in-memory per-server index: port.ServerDomainRepository
// over model.RegisteredDomain, port.ServerStorageRepository over
// model.StorageSummary.
type fakeIndex[T any] struct {
	rows     map[string][]T // serverID -> rows
	upserted []T
	deleted  []string
}

type (
	fakeServerDomains  = fakeIndex[model.RegisteredDomain]
	fakeServerStorages = fakeIndex[model.StorageSummary]
)

// newFakeIndex holds rows for srv-1.
func newFakeIndex[T any](rows ...T) *fakeIndex[T] {
	f := &fakeIndex[T]{rows: map[string][]T{}}
	if len(rows) > 0 {
		f.rows["srv-1"] = rows
	}
	return f
}

func newFakeServerDomains(rows ...model.RegisteredDomain) *fakeServerDomains {
	return newFakeIndex(rows...)
}
func newFakeServerStorages(rows ...model.StorageSummary) *fakeServerStorages {
	return newFakeIndex(rows...)
}

func (f *fakeIndex[T]) ListForServer(_ context.Context, serverID string) ([]T, error) {
	return f.rows[serverID], nil
}
func (f *fakeIndex[T]) Upsert(_ context.Context, serverID string, row T) error {
	f.upserted = append(f.upserted, row)
	f.rows[serverID] = append(f.rows[serverID], row)
	return nil
}
func (f *fakeIndex[T]) Delete(_ context.Context, _, name string) error {
	f.deleted = append(f.deleted, name)
	return nil
}
func (f *fakeIndex[T]) ReplaceForServer(_ context.Context, serverID string, rows []T) error {
	f.rows[serverID] = rows
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
	sd := newFakeServerDomains(model.RegisteredDomain{Name: "stale.app", TLS: model.DomainTLSAuto})
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

	req := command.CreateDomainRequest{Name: "*.x.app", TLS: model.DomainTLSOff}
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
	del.OnResult(port.CommandResult{Success: false, Error: "*.x.app is used by Shop"})
	if len(sd.deleted) != 0 {
		t.Fatal("a refused delete left the index")
	}
	del.OnResult(port.CommandResult{Success: true})
	if len(sd.deleted) != 1 || sd.deleted[0] != "*.x.app" {
		t.Fatalf("deleted = %v", sd.deleted)
	}
}

// The index is filled when the agent comes online (bootstrap), not by
// every apps reconcile.
func TestRefreshAppsDoesNotListDomains(t *testing.T) {
	disp := &recordingDispatcher{}
	uc := newDomainsUseCase(disp, newFakeServerDomains())
	if _, err := uc.RefreshApps(context.Background(), "u1", "srv-1"); err != nil {
		t.Fatal(err)
	}
	disp.last().OnResult(port.CommandResult{Success: true, Payload: []byte(`{"apps":[]}`)})
	if len(disp.all) != 1 {
		t.Fatalf("dispatches = %+v", disp.all)
	}
}

func TestServerDomainsReadsTheIndex(t *testing.T) {
	ctx := context.Background()
	sd := newFakeServerDomains(model.RegisteredDomain{Name: "*.x.app", TLS: model.DomainTLSOff})
	got, err := newDomainsUseCase(&recordingDispatcher{}, sd).ServerDomains(ctx, "srv-1")
	if err != nil || len(got) != 1 || got[0].Name != "*.x.app" {
		t.Fatalf("ServerDomains = %+v, %v", got, err)
	}
	// Without an index: an empty list, never null.
	got, err = newTestUseCase(&recordingDispatcher{}).ServerDomains(ctx, "srv-1")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("no index = %#v, %v", got, err)
	}
}

func TestDomainResultsWithoutAnIndexAreNoOps(t *testing.T) {
	disp := &recordingDispatcher{}
	uc := newTestUseCase(disp) // no ServerDomainRepository
	ctx := context.Background()
	_, _ = uc.ListServerDomains(ctx, "u", "s")
	_, _ = uc.CreateDomain(ctx, "u", "s", command.CreateDomainRequest{Name: "x.app", TLS: model.DomainTLSOff})
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
