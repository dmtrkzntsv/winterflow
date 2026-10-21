package bootstrap

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/hubs"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

type staticHubs []model.Hub

func (s staticHubs) Announce(context.Context, model.Hub, time.Duration) error { return nil }
func (s staticHubs) Withdraw(context.Context, string) error                  { return nil }
func (s staticHubs) List(context.Context) ([]model.Hub, error)               { return s, nil }

// With a hub registry WireCore routes commands to each server's hub queue
// and builds the picker; without one (standalone) neither.
func TestWireCoreHubRouting(t *testing.T) {
	t.Setenv("DATABASE_URL", "sqlite://"+filepath.Join(t.TempDir(), "wf.sqlite"))
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := db.Open(log, cfg.GetDbURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Shutdown() })
	srv := dbtest.ID(11)
	dbtest.SeedServer(t, conn, dbtest.ID(1), srv)
	dbtest.Exec(t, conn, `UPDATE servers SET hub_id = ? WHERE server_id = ?`, "hub-01.winterflow.dev", srv)

	b := membus.NewBus(log)
	msgs, stop, err := b.Subscribe(ctx, cfg.HubRequestQueue("hub-01.winterflow.dev"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stop() }()

	deps, _ := WireCore(ctx, b, conn, cfg, log, WithHubRegistry(staticHubs{{ID: "hub-01.winterflow.dev", Capacity: 10, Accepting: true}}))
	if deps.HubPicker == nil {
		t.Fatal("no picker")
	}
	if h, err := deps.HubPicker.Pick(ctx); err != nil || h.ID != "hub-01.winterflow.dev" || h.Assigned != 1 {
		t.Fatalf("pick = %+v, %v", h, err)
	}
	if _, err := deps.CommandDispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: srv, UserID: "u1", Type: command.TypeAppsList, Payload: command.ListAppsRequest{},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-msgs:
	case <-time.After(2 * time.Second):
		t.Fatal("command not published on the hub's queue")
	}

	// A server without a hub: the dispatch fails with the classed error.
	other := dbtest.ID(12)
	dbtest.SeedServer(t, conn, dbtest.ID(2), other)
	_, err = deps.CommandDispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: other, UserID: "u1", Type: command.TypeAppsList, Payload: command.ListAppsRequest{},
	})
	if !errors.Is(err, hubs.ErrServerHasNoHub) {
		t.Fatalf("want ErrServerHasNoHub, got %v", err)
	}

	standalone, _ := WireCore(ctx, membus.NewBus(log), conn, cfg, log)
	if standalone.HubPicker != nil {
		t.Fatal("standalone has a picker")
	}
}
