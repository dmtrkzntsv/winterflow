package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/codec"
	"winterflow/internal/infra/transport/grpc/proto"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// The in-process bridge must consume a command from the request queue, run it
// through the dispatcher, and publish a correlated notification on the
// response queue — the standalone analogue of the hub round-trip.
func TestInProcessBridgeRoundTrip(t *testing.T) {
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	b := membus.NewBus(log)

	d := NewDispatcher(dockercompose.NewRepository(cfg, log), nil, log)
	bridge := NewInProcessBridge(b, cfg, d, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	results, cancelSub, err := b.Subscribe(ctx, cfg.GetBusResponseQueue())
	if err != nil {
		t.Fatal(err)
	}
	defer cancelSub()

	if err := bridge.Start(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	// apps.list against an empty data dir succeeds with an empty list.
	err = b.Publish(ctx, cfg.GetBusRequestQueue(), bus.CommandMessage{
		AgentID:   "local",
		RequestID: "req-77",
		Type:      string(command.TypeAppsList),
		Payload:   []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-results:
		var ntf model.Notification
		if err := json.Unmarshal([]byte(msg.Payload), &ntf); err != nil {
			t.Fatal(err)
		}
		if ntf.Ref != "req-77" {
			t.Fatalf("ref = %q", ntf.Ref)
		}
		if ntf.Status != model.NotificationStatusSuccess {
			t.Fatalf("status = %v, error = %q", ntf.Status, ntf.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no result on the response queue")
	}
}

func TestInProcessBridgeErrorsStayCorrelated(t *testing.T) {
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	b := membus.NewBus(log)
	bridge := NewInProcessBridge(b, cfg, NewDispatcher(dockercompose.NewRepository(cfg, log), nil, log), log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results, cancelSub, _ := b.Subscribe(ctx, cfg.GetBusResponseQueue())
	defer cancelSub()
	_ = bridge.Start(ctx)
	time.Sleep(20 * time.Millisecond)

	_ = b.Publish(ctx, cfg.GetBusRequestQueue(), bus.CommandMessage{
		AgentID:   "local",
		RequestID: "req-bad",
		Type:      "no.such.command",
	})

	select {
	case msg := <-results:
		var ntf model.Notification
		_ = json.Unmarshal([]byte(msg.Payload), &ntf)
		if ntf.Ref != "req-bad" || ntf.Status != model.NotificationStatusError {
			t.Fatalf("notification = %+v", ntf)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no error result")
	}
}

// blockingRunner holds backup.run until released and answers everything
// else at once.
type blockingRunner struct{ release chan struct{} }

func (b blockingRunner) Dispatch(_ context.Context, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	if command.Type(req.Type) == command.TypeBackupRun {
		<-b.release
	}
	resp, _ := codec.EncodeResponse("local", req.RequestId, command.Type(req.Type), proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok", struct{}{})
	return resp
}

// A backup.run waiting for its run never holds up the commands behind it.
func TestInProcessBridgeDetachesBackupRuns(t *testing.T) {
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	b := membus.NewBus(log)
	runner := blockingRunner{release: make(chan struct{})}
	bridge := NewInProcessBridge(b, cfg, runner, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results, cancelSub, _ := b.Subscribe(ctx, cfg.GetBusResponseQueue())
	defer cancelSub()
	_ = bridge.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	for _, c := range []bus.CommandMessage{
		{AgentID: "local", RequestID: "run", Type: string(command.TypeBackupRun), Payload: []byte(`{}`)},
		{AgentID: "local", RequestID: "list", Type: string(command.TypeAppsList), Payload: []byte(`{}`)},
	} {
		if err := b.Publish(ctx, cfg.GetBusRequestQueue(), c); err != nil {
			t.Fatal(err)
		}
	}
	next := func() string {
		select {
		case msg := <-results:
			var ntf model.Notification
			_ = json.Unmarshal([]byte(msg.Payload), &ntf)
			return ntf.Ref
		case <-time.After(2 * time.Second):
			t.Fatal("no result")
			return ""
		}
	}
	if ref := next(); ref != "list" {
		t.Fatalf("first result = %s: the bridge waited for backup.run", ref)
	}
	close(runner.release)
	if ref := next(); ref != "run" {
		t.Fatalf("second result = %s", ref)
	}
}
