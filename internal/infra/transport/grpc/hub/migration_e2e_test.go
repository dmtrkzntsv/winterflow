package hub_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	hagenthub "winterflow/internal/app/web/handler/agenthub"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/agentauth"
	"winterflow/internal/domain/usecase/agenthub"
	"winterflow/internal/domain/usecase/hubs"
	"winterflow/internal/infra/cert"
	"winterflow/internal/infra/transport/bus"
	grpcagent "winterflow/internal/infra/transport/grpc/agent"
	"winterflow/internal/infra/transport/grpc/hub"
	"winterflow/internal/infra/transport/grpc/proto"
	membus "winterflow/internal/infra/transport/mem/bus"
	hubsreg "winterflow/internal/infra/transport/redis/hubs"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// serverStore is the servers table as the API's agent auth responder and
// agent hub usecase read it: one server, its active certificate and its
// hub, moved by compare-and-set like the SQL repository.
type serverStore struct {
	mu       sync.Mutex
	serverID string
	certPEM  string
	hubID    string
}

func (s *serverStore) ServerHub(_ context.Context, id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.serverID {
		return "", model.ErrNotFound
	}
	return s.hubID, nil
}

func (s *serverStore) ActiveServerCertificate(_ context.Context, id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.serverID {
		return "", model.ErrNotFound
	}
	return s.certPEM, nil
}

func (s *serverStore) SetServerHub(_ context.Context, id, from, to string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.serverID {
		return "", model.ErrNotFound
	}
	if s.hubID == from {
		s.hubID = to
	}
	return s.hubID, nil
}

func (s *serverStore) CountServersByHub(context.Context) (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]int{s.hubID: 1}, nil
}

func (s *serverStore) hub() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hubID
}

// okDispatcher answers every command with success.
type okDispatcher struct{}

func (okDispatcher) Dispatch(_ context.Context, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	return &proto.ResponseEnvelope{
		Base:      &proto.BaseResponse{AgentId: req.GetBase().GetAgentId(), ResponseCode: proto.ResponseCode_RESPONSE_CODE_SUCCESS},
		RequestId: req.GetRequestId(),
		Type:      req.GetType(),
		Payload:   []byte(`{}`),
	}
}

// startListedHub runs a hub as cmd/hub does: public address host:<its
// port>, the bus authorizer (answered by the API's responder), the bus
// bridge, the announcer and the drain watch over reg.
func startListedHub(t *testing.T, ctx context.Context, log *logger.Logger, cfg *config.ServerConfig, b bus.Bus, reg *hubsreg.Registry, host string) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(lis.Addr().String())
	addr := net.JoinHostPort(host, port)
	// NewHub reads its identity once; the other hub sets its own after.
	t.Setenv("HUB_PUBLIC_ADDR", addr)
	auth := hub.NewBusAuthorizer(b, cfg, host, 2*time.Second, log)
	if err := auth.Start(ctx); err != nil {
		t.Fatal(err)
	}
	h := hub.NewHub(log, cfg, b, auth)
	hub.SetDrainCheckEvery(h, 50*time.Millisecond)
	if err := h.StartBusBridge(ctx); err != nil {
		t.Fatal(err)
	}
	hctx, stop := context.WithCancel(ctx)
	announced := h.StartAnnouncer(hctx, reg)
	watched := h.StartDrainWatch(hctx, reg)
	go func() { _ = h.Serve(lis) }()
	t.Cleanup(func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = h.Shutdown(shutdownCtx)
		stop()
		<-announced
		<-watched
	})
	return addr
}

// roundTrip sends one command through queue and returns how it ended.
func roundTrip(t *testing.T, ctx context.Context, b bus.Bus, responses <-chan bus.Message, queue, agentID, requestID string) model.Notification {
	t.Helper()
	if err := b.Publish(ctx, queue, bus.CommandMessage{
		AgentID: agentID, RequestID: requestID, Type: string(command.TypeAppsList), Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case msg := <-responses:
			var ntf model.Notification
			if err := json.Unmarshal([]byte(msg.Payload), &ntf); err != nil {
				t.Fatal(err)
			}
			if ntf.Ref == requestID {
				return ntf
			}
		case <-deadline:
			t.Fatalf("no result for %s", requestID)
		}
	}
}

// waitFor polls cond until it holds or 15 s pass.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The whole migration path with real parts: the agent's Run loop on hub A,
// hub A drained through its Redis flag, the hub telling the agent to move,
// the agent asking the API's signed agent hub route, the API's picker
// moving the server to hub B, the agent storing hub.json and streaming on
// hub B, whose auth check the API's responder allows. Hub A answers
// "moved" for the server afterwards.
func TestAgentMigratesThroughTheAPI(t *testing.T) {
	dir := t.TempDir()
	extPath := filepath.Join(dir, "ext.cnf")
	if err := os.WriteFile(extPath, []byte(extCnf), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HUB_CERT_DIR", filepath.Join(dir, "certs"))
	t.Setenv("HUB_CERT_EXT_PATH", extPath)
	t.Setenv("HUB_CA_SUBJECT", "/C=CA/O=Test/CN=Test CA")
	t.Setenv("HUB_SERVER_SUBJECT", "/C=CA/O=Test/CN=localhost")
	dataDir := filepath.Join(dir, "agent-data")
	t.Setenv("AGENT_DATA_DIR", dataDir)
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "e2e"})
	cfg := config.NewServerConfig("distributed")
	cm, err := cert.NewManager(cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	cm.GenerateServer(true)
	const agentID = "srv-migrate"
	if _, err := cm.GenerateAgent(agentID); err != nil {
		t.Fatal(err)
	}
	certPEM, err := os.ReadFile(cfg.GetAgentCertPath())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b := membus.NewBus(log)
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	reg := hubsreg.NewRegistry(rc)

	// Hub ids are the hosts: "localhost" (A) and "127.0.0.1" (B), both in
	// the hub certificate.
	const hubA, hubB = "localhost", "127.0.0.1"
	store := &serverStore{serverID: agentID, certPEM: string(certPEM), hubID: hubA}

	// The API: the agent auth responder on the bus, and the agent hub route
	// over the live hub list.
	if err := agentauth.StartResponder(ctx, b, cfg, store, log); err != nil {
		t.Fatal(err)
	}
	locator := agenthub.New(store, reg, hubs.NewPicker(reg, store, log), log)
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+hagenthub.HubPath, hagenthub.NewHandler(&hagenthub.Deps{Logger: log, Locator: locator}).Where)
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)

	addrA := startListedHub(t, ctx, log, cfg, b, reg, hubA)
	addrB := startListedHub(t, ctx, log, cfg, b, reg, hubB)
	waitFor(t, "both hubs listed", func() bool {
		listed, _ := reg.List(ctx)
		return len(listed) == 2
	})

	responses, cancelResponses, err := b.Subscribe(ctx, cfg.GetBusResponseQueue())
	if err != nil {
		t.Fatal(err)
	}
	defer cancelResponses()

	// The agent starts on hub A (its env's HUB_HOST/HUB_PORT, no hub.json).
	_, portA, _ := net.SplitHostPort(addrA)
	t.Setenv("HUB_HOST", hubA)
	t.Setenv("HUB_PORT", portA)
	t.Setenv("WINTERFLOW_API_URL", api.URL)
	agent := grpcagent.NewAgent(log, cfg, agentID)
	agent.SetDispatcher(okDispatcher{})
	runCtx, stopAgent := context.WithCancel(ctx)
	ran := make(chan error, 1)
	go func() { ran <- agent.Run(runCtx) }()
	t.Cleanup(func() {
		stopAgent()
		<-ran
		stopCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = agent.Stop(stopCtx)
	})

	// A command routed to hub A reaches it (the hub binds the stream on its
	// first heartbeat; until then it answers "agent not connected").
	delivered := func(hubID string) func() bool {
		n := 0
		return func() bool {
			n++
			id := fmt.Sprintf("probe-%s-%d", hubID, n)
			return roundTrip(t, ctx, b, responses, cfg.HubRequestQueue(hubID), agentID, id).Status == model.NotificationStatusSuccess
		}
	}
	waitFor(t, "the agent's stream on hub A", delivered(hubA))

	// Drain hub A.
	if err := reg.SetDraining(ctx, hubA, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the API to move the server to hub B", func() bool { return store.hub() == hubB })
	waitFor(t, "hub.json naming hub B", func() bool {
		raw, err := os.ReadFile(filepath.Join(dataDir, "hub.json"))
		if err != nil {
			return false
		}
		var f struct{ Host, Port string }
		return json.Unmarshal(raw, &f) == nil && net.JoinHostPort(f.Host, f.Port) == addrB
	})

	// The agent streams on hub B: a command routed to B's queue reaches it
	// (B registered it, so the responder allowed its auth check).
	waitFor(t, "the agent's stream on hub B", delivered(hubB))

	// Undrained, hub A checks the server again and answers "moved".
	if err := reg.SetDraining(ctx, hubA, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "hub A accepting again", func() bool {
		listed, _ := reg.List(ctx)
		for _, h := range listed {
			if h.ID == hubA {
				return !h.Draining
			}
		}
		return false
	})
	err = register(ctx, dialAddr(t, cfg, addrA), agentID)
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(status.Convert(err).Message(), "another hub") {
		t.Fatalf("hub A register err = %v, want FailedPrecondition (moved)", err)
	}
}
