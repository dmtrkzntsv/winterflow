package hub_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	appagent "winterflow/internal/app/agent"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/cert"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/internal/infra/transport/bus"
	grpcagent "winterflow/internal/infra/transport/grpc/agent"
	"winterflow/internal/infra/transport/grpc/hub"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

const extCnf = `[v3_ext]
basicConstraints = CA:FALSE
keyUsage         = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth

subjectAltName   = @alt_names

[alt_names]
DNS.1 = localhost
IP.1 = 127.0.0.1
`

// freePort grabs an ephemeral port and releases it for the hub to bind.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return fmt.Sprintf("%d", l.Addr().(*net.TCPAddr).Port)
}

func recvEvent(t *testing.T, ch <-chan bus.Message, wantKind bus.EventKind) bus.EventMessage {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case msg := <-ch:
			var ev bus.EventMessage
			if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
				t.Fatalf("bad event payload %q: %v", msg.Payload, err)
			}
			if ev.Kind == wantKind {
				return ev
			}
			// Skip interleaved kinds (e.g. server.online pulses).
		case <-deadline:
			t.Fatalf("timed out waiting for event kind %q", wantKind)
		}
	}
}

// TestHubAgentRoundTripOverMTLS runs the real distributed path in-process:
// cert generation → hub with mTLS → agent connect/register/stream → command
// dispatch over the bus through the stream and back → agent-initiated event.
func TestHubAgentRoundTripOverMTLS(t *testing.T) {
	dir := t.TempDir()
	extPath := filepath.Join(dir, "ext.cnf")
	if err := os.WriteFile(extPath, []byte(extCnf), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HUB_CERT_DIR", filepath.Join(dir, "certs"))
	t.Setenv("HUB_CERT_EXT_PATH", extPath)
	t.Setenv("HUB_CA_SUBJECT", "/C=CA/O=Test/CN=Test CA")
	t.Setenv("HUB_SERVER_SUBJECT", "/C=CA/O=Test/CN=localhost")
	t.Setenv("HUB_HOST", "127.0.0.1")
	t.Setenv("HUB_PORT", freePort(t))
	t.Setenv("AGENT_DATA_DIR", filepath.Join(dir, "agent-data"))

	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "e2e"})
	cfg := config.NewServerConfig("distributed")

	cm, err := cert.NewManager(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	cm.GenerateServer(true)
	if _, err := cm.GenerateAgent("cert-e2e"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := membus.NewBus(log)
	events, cancelEvents, err := b.Subscribe(ctx, cfg.GetBusEventsQueue())
	if err != nil {
		t.Fatal(err)
	}
	defer cancelEvents()
	responses, cancelResponses, err := b.Subscribe(ctx, cfg.GetBusResponseQueue())
	if err != nil {
		t.Fatal(err)
	}
	defer cancelResponses()

	h := hub.NewHub(log, cfg, b)
	if err := h.StartBusBridge(ctx); err != nil {
		t.Fatal(err)
	}
	go func() { _ = h.ListenAndServe(ctx) }()
	t.Cleanup(func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = h.Shutdown(shutdownCtx)
	})
	time.Sleep(100 * time.Millisecond) // let the listener bind

	// Agent side: real mTLS client, real dispatcher over a temp data dir.
	agent := grpcagent.NewAgent(log, cfg, "agent-e2e")
	agent.SetCapabilities(map[string]string{"os": "linux", "version": "test"})
	agent.SetFeatures(map[string]bool{"can_install": true})
	agent.SetDispatcher(appagent.NewDispatcher(dockercompose.NewRepository(cfg, log), nil, log))

	if err := agent.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := agent.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if err := agent.StartStream(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = agent.Stop(stopCtx)
	})

	// 1. Registration published the agent's capabilities.
	capsEv := recvEvent(t, events, bus.EventCapabilities)
	if capsEv.ServerID != "agent-e2e" {
		t.Fatalf("capabilities event from %q", capsEv.ServerID)
	}
	var caps struct {
		Capabilities map[string]string `json:"capabilities"`
	}
	if err := json.Unmarshal(capsEv.Payload, &caps); err != nil || caps.Capabilities["os"] != "linux" {
		t.Fatalf("capabilities payload = %s (%v)", capsEv.Payload, err)
	}

	// 2. The immediate first heartbeat produced a liveness pulse (and marked
	// the stream active, so commands can be dispatched right away).
	if ev := recvEvent(t, events, bus.EventServerOnline); ev.ServerID != "agent-e2e" {
		t.Fatalf("online event from %q", ev.ServerID)
	}

	// 3. Full command round-trip: request queue → stream → dispatcher →
	// response queue, correlated by request id.
	if err := b.Publish(ctx, cfg.GetBusRequestQueue(), bus.CommandMessage{
		AgentID:   "agent-e2e",
		RequestID: "req-e2e-1",
		Type:      string(command.TypeAppsList),
		Payload:   []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-responses:
		var ntf model.Notification
		if err := json.Unmarshal([]byte(msg.Payload), &ntf); err != nil {
			t.Fatal(err)
		}
		if ntf.Ref != "req-e2e-1" || ntf.Status != model.NotificationStatusSuccess {
			t.Fatalf("notification = %+v", ntf)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no command result on the response queue")
	}

	// 4. Agent-initiated event rides the stream to the events queue.
	statusPayload, _ := json.Marshal(command.GetAppsStatusResponse{
		Apps: []command.AppStatus{{AppID: "a1", StatusCode: command.ContainerStatusActive}},
	})
	if err := agent.SendEvent(string(bus.EventAppsStatus), statusPayload); err != nil {
		t.Fatal(err)
	}
	appsEv := recvEvent(t, events, bus.EventAppsStatus)
	if appsEv.ServerID != "agent-e2e" {
		t.Fatalf("apps status event from %q", appsEv.ServerID)
	}
	var body command.GetAppsStatusResponse
	if err := json.Unmarshal(appsEv.Payload, &body); err != nil || len(body.Apps) != 1 || body.Apps[0].AppID != "a1" {
		t.Fatalf("apps status payload = %s (%v)", appsEv.Payload, err)
	}
}
