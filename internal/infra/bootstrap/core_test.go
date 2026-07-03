package bootstrap

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// wireCore is the shared wiring both topologies boot through; prove it
// produces a working Deps whose response subscriber actually drains the
// response queue into the notification manager.
func TestWireCoreWiresResponsePipeline(t *testing.T) {
	t.Setenv("DATABASE_URL", "sqlite://"+filepath.Join(t.TempDir(), "wf.sqlite"))
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := membus.NewBus(log)
	conn := db.NewBunConnection(log, cfg.GetDbURL())
	t.Cleanup(func() { _ = conn.Shutdown() })

	deps, serverRepo := wireCore(ctx, b, conn, cfg, log)
	if deps.NotificationManager == nil || deps.CommandDispatcher == nil ||
		deps.StatusCache == nil || deps.ServerRepository == nil || serverRepo == nil {
		t.Fatalf("incomplete deps: %+v", deps)
	}

	// A notification published on the response queue must reach an SSE
	// channel via dispatcher HandleResult → NotificationManager. The dispatch
	// manager routes by request id; without a pending dispatch the message is
	// dropped — so go through the dispatcher to create the pending entry.
	ch := deps.NotificationManager.AddChannel("u1")
	t.Cleanup(func() { deps.NotificationManager.RemoveChannel("u1", ch) })

	reqID, err := deps.CommandDispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: "srv-1",
		UserID:  "u1",
		Type:    command.TypeAppsList,
		Payload: command.ListAppsRequest{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, cfg.GetBusResponseQueue(), model.Notification{
		Type:      model.NotificationOperationResult,
		Ref:       reqID,
		Status:    model.NotificationStatusSuccess,
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case raw := <-ch:
		if raw == "" {
			t.Fatal("empty notification")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("response never reached the user channel")
	}
}
