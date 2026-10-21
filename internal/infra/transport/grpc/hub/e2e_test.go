package hub_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
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
	"winterflow/internal/infra/transport/grpc/proto"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
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

// allowAll is the e2e tests' authorizer when the API's answer is not what
// they exercise.
type allowAll struct{}

func (allowAll) Authorize(context.Context, string, string) (bus.AgentVerdict, string, error) {
	return bus.VerdictAllowed, "", nil
}

type e2eEnv struct {
	log *logger.Logger
	cfg *config.ServerConfig
	bus bus.Bus
	ctx context.Context
}

// startHub generates a CA, a hub cert and an agent cert whose CN is agentCN,
// then runs a hub with mTLS over an in-process bus. auth nil means a
// BusAuthorizer over that bus with a short timeout.
func startHub(t *testing.T, agentCN string, auth hub.Authorizer) *e2eEnv {
	t.Helper()
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
	t.Setenv("HUB_PUBLIC_ADDR", "hub-01.winterflow.dev:50051")
	t.Setenv("AGENT_DATA_DIR", filepath.Join(dir, "agent-data"))

	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "e2e"})
	cfg := config.NewServerConfig("distributed")

	cm, err := cert.NewManager(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	cm.GenerateServer(true)
	if _, err := cm.GenerateAgent(agentCN); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	b := membus.NewBus(log)
	if auth == nil {
		ba := hub.NewBusAuthorizer(b, cfg, cfg.GetHubID(), 300*time.Millisecond, log)
		if err := ba.Start(ctx); err != nil {
			t.Fatal(err)
		}
		auth = ba
	}
	h := hub.NewHub(log, cfg, b, auth)
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
	return &e2eEnv{log: log, cfg: cfg, bus: b, ctx: ctx}
}

// TestHubAgentRoundTripOverMTLS runs the real distributed path in-process:
// cert generation → hub with mTLS → agent connect/register/stream → command
// dispatch over the bus through the stream and back → agent-initiated event.
func TestHubAgentRoundTripOverMTLS(t *testing.T) {
	env := startHub(t, "agent-e2e", allowAll{})
	log, cfg, b, ctx := env.log, env.cfg, env.bus, env.ctx

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
	if err := b.Publish(ctx, cfg.HubRequestQueue(cfg.GetHubID()), bus.CommandMessage{
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

// dialHub opens a raw mTLS client with the agent certificate, so a test can
// send what a real agent never would.
func dialHub(t *testing.T, cfg *config.ServerConfig) proto.AgentServiceClient {
	t.Helper()
	caPEM, err := os.ReadFile(cfg.GetAgentCACertPath())
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	pair, err := tls.LoadX509KeyPair(cfg.GetAgentCertPath(), cfg.GetAgentKeyPath())
	if err != nil {
		t.Fatal(err)
	}
	creds := credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{pair}, RootCAs: pool, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2"}})
	conn, err := grpc.NewClient(cfg.GetHubHost()+":"+cfg.GetHubPort(), grpc.WithTransportCredentials(creds))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return proto.NewAgentServiceClient(conn)
}

func register(ctx context.Context, c proto.AgentServiceClient, agentID string) error {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.RegisterAgent(cctx, &proto.RegisterAgentRequest{Base: &proto.BaseMessage{MessageId: "r", AgentId: agentID}})
	return err
}

// answerVerify plays the API's agent-auth responder with a fixed verdict.
func answerVerify(t *testing.T, env *e2eEnv, verdict bus.AgentVerdict, reason string) {
	t.Helper()
	bus.SubscribeJSON(env.ctx, env.bus, env.cfg.GetHubRequestsChannel(), env.log, func(req bus.HubRequest) {
		_ = env.bus.Publish(env.ctx, config.HubResponsesChannel(req.HubID), bus.HubResponse{RequestID: req.RequestID, Verdict: verdict, Reason: reason})
	})
}

func TestRegisterRejectsMismatchedAgentID(t *testing.T) {
	env := startHub(t, "srv-cert", allowAll{})
	err := register(env.ctx, dialHub(t, env.cfg), "srv-other")
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied", err)
	}
}

func TestRegisterDeniedForUnknownServer(t *testing.T) {
	env := startHub(t, "srv-1", nil)
	answerVerify(t, env, bus.VerdictUnknown, "server not found")
	err := register(env.ctx, dialHub(t, env.cfg), "srv-1")
	if status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "server not found") {
		t.Fatalf("err = %v, want PermissionDenied with the reason", err)
	}
}

func TestRegisterAllowedByResponder(t *testing.T) {
	env := startHub(t, "srv-1", nil)
	answerVerify(t, env, bus.VerdictAllowed, "")
	if err := register(env.ctx, dialHub(t, env.cfg), "srv-1"); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterUnavailableWithoutResponder(t *testing.T) {
	env := startHub(t, "srv-1", nil)
	err := register(env.ctx, dialHub(t, env.cfg), "srv-1")
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("err = %v, want Unavailable", err)
	}
}

func TestStreamRejectsForeignAgentID(t *testing.T) {
	env := startHub(t, "srv-1", allowAll{})
	events, cancelEvents, err := env.bus.Subscribe(env.ctx, env.cfg.GetBusEventsQueue())
	if err != nil {
		t.Fatal(err)
	}
	defer cancelEvents()

	c := dialHub(t, env.cfg)
	if err := register(env.ctx, c, "srv-1"); err != nil {
		t.Fatal(err)
	}
	sctx, cancel := context.WithTimeout(env.ctx, 5*time.Second)
	defer cancel()
	stream, err := c.AgentStream(sctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&proto.AgentMessage{Message: &proto.AgentMessage_Event{Event: &proto.AgentEvent{
		Base: &proto.BaseMessage{AgentId: "srv-victim"},
		Kind: string(bus.EventAppsStatus),
	}}}); err != nil {
		t.Fatal(err)
	}
	_, err = stream.Recv()
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied", err)
	}
	// Registration published capabilities; the foreign event must not follow.
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case msg := <-events:
			var ev bus.EventMessage
			_ = json.Unmarshal([]byte(msg.Payload), &ev)
			if ev.Kind == bus.EventAppsStatus {
				t.Fatalf("foreign event published: %+v", ev)
			}
		case <-deadline:
			return
		}
	}
}

func TestRevokeClosesStream(t *testing.T) {
	env := startHub(t, "srv-1", allowAll{})
	responses, cancelResponses, err := env.bus.Subscribe(env.ctx, env.cfg.GetBusResponseQueue())
	if err != nil {
		t.Fatal(err)
	}
	defer cancelResponses()

	agent := grpcagent.NewAgent(env.log, env.cfg, "srv-1")
	agent.SetDispatcher(appagent.NewDispatcher(dockercompose.NewRepository(env.cfg, env.log), nil, env.log))
	if err := agent.Connect(env.ctx); err != nil {
		t.Fatal(err)
	}
	if err := agent.Register(env.ctx); err != nil {
		t.Fatal(err)
	}
	if err := agent.StartStream(env.ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = agent.Stop(stopCtx)
	})
	time.Sleep(200 * time.Millisecond) // first heartbeat binds the stream

	// A broadcast for another server changes nothing.
	if err := env.bus.Publish(env.ctx, env.cfg.GetHubBroadcastChannel(), bus.HubBroadcast{Type: bus.HubBroadcastRevoked, ServerID: "srv-2"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if !agent.IsStreamActive() {
		t.Fatal("stream closed by another server's revocation")
	}

	if err := env.bus.Publish(env.ctx, env.cfg.GetHubBroadcastChannel(), bus.HubBroadcast{Type: bus.HubBroadcastRevoked, ServerID: "srv-1"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for agent.IsStreamActive() {
		if time.Now().After(deadline) {
			t.Fatal("stream still active after revocation")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The registration is gone: a command bounces as not connected.
	if err := env.bus.Publish(env.ctx, env.cfg.HubRequestQueue(env.cfg.GetHubID()), bus.CommandMessage{
		AgentID: "srv-1", RequestID: "req-after-revoke", Type: string(command.TypeAppsList), Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-responses:
		var ntf model.Notification
		if err := json.Unmarshal([]byte(msg.Payload), &ntf); err != nil {
			t.Fatal(err)
		}
		if ntf.Ref != "req-after-revoke" || ntf.Error != bus.ErrAgentNotConnected {
			t.Fatalf("notification = %+v", ntf)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no result for the command")
	}
}
