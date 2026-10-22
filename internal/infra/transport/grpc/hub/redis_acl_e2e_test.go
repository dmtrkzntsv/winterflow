package hub_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/agentauth"
	"winterflow/internal/infra/bootstrap"
	"winterflow/internal/infra/transport/bus"
	grpcagent "winterflow/internal/infra/transport/grpc/agent"
	"winterflow/internal/infra/transport/grpc/hub"
	redisbus "winterflow/internal/infra/transport/redis/bus"
	hubsreg "winterflow/internal/infra/transport/redis/hubs"
	"winterflow/internal/infra/transport/redis/hubusers"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// The hub as cmd/hub runs it (BootstrapHUB: its Redis client, bus,
// authorizer and registry; the bus bridge, the announcer, the drain watch
// and the stop's drain), logged in to a real Redis as the ACL user the API
// made for it (hubusers.Ensure), with a real agent on it and the API's side
// (the agent auth responder, commands, replies, events) on the admin
// connection. Nothing the hub does in normal operation is refused: the ACL
// log holds no entry for its user. Needs make test-redis.
func TestIntegrationHubRunsOnItsRedisUser(t *testing.T) {
	addr := os.Getenv("WF_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("WF_TEST_REDIS_ADDR not set (run make test-redis)")
	}
	admin := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("WF_TEST_REDIS_PASSWORD")})
	t.Cleanup(func() { _ = admin.Close() })

	dir := t.TempDir()
	t.Setenv("HUB_CERT_DIR", filepath.Join(dir, "certs"))
	t.Setenv("AGENT_DATA_DIR", filepath.Join(dir, "agent-data"))
	// hub.move makes the agent ask the API where to go; nobody answers here.
	t.Setenv("WINTERFLOW_API_URL", "http://127.0.0.1:1")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "e2e"})
	cfg := config.NewServerConfig("distributed")
	issueHubCert(t, cfg)
	const agentID = "srv-redis-acl"
	issueAgentCert(t, cfg, agentID)
	certPEM, err := os.ReadFile(cfg.GetAgentCertPath())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	// The hub's user, as a join or hub-trust makes it.
	const hubID = "localhost"
	users := hubusers.New(admin)
	username, password, err := users.Ensure(ctx, hubID)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	t.Cleanup(func() { _ = users.Delete(context.Background(), hubID) })
	if err := admin.ACLLogReset(ctx).Err(); err != nil {
		t.Fatal(err)
	}

	// The API: the agent auth responder, commands and the reply/event
	// queues, over the admin connection.
	apiBus := redisbus.NewBus(admin, log)
	store := &serverStore{serverID: agentID, certPEM: string(certPEM), hubID: hubID}
	if err := agentauth.StartResponder(ctx, apiBus, cfg, store, log); err != nil {
		t.Fatal(err)
	}
	responses, stopResponses, err := apiBus.Subscribe(ctx, cfg.GetBusResponseQueue())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stopResponses() }()
	events, stopEvents, err := apiBus.Subscribe(ctx, cfg.GetBusEventsQueue())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stopEvents() }()

	// The hub, from its env as the installer writes it (TLS is off on the
	// test server; the ACL rules do not depend on it).
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(lis.Addr().String())
	t.Setenv("HUB_PUBLIC_ADDR", net.JoinHostPort(hubID, port))
	t.Setenv("REDIS_ADDR", addr)
	t.Setenv("REDIS_TLS", "false")
	t.Setenv("REDIS_USERNAME", username)
	t.Setenv("REDIS_PASSWORD", password)
	t.Setenv("REDIS_DB", "")
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	deps := bootstrap.BootstrapHUB(runCtx, log, cfg)
	h := hub.NewHub(log, cfg, deps.Bus, deps.Auth)
	hub.SetDrainCheckEvery(h, 50*time.Millisecond)
	if err := h.StartBusBridge(runCtx); err != nil {
		t.Fatal(err)
	}
	announced := h.StartAnnouncer(runCtx, deps.Registry)
	watched := h.StartDrainWatch(runCtx, deps.Registry)
	served := make(chan struct{})
	go func() { defer close(served); _ = h.Serve(lis) }()
	stopped := false
	stopHub := func() {
		if stopped {
			return
		}
		stopped = true
		shutdownCtx, c := context.WithTimeout(context.Background(), 20*time.Second)
		defer c()
		if err := h.Shutdown(shutdownCtx); err != nil {
			t.Errorf("hub shutdown: %v", err)
		}
		cancelRun()
		<-announced
		<-watched
		<-served
	}
	t.Cleanup(stopHub)

	adminReg := hubsreg.NewRegistry(admin)
	listed := func(want func(model.Hub) bool) func() bool {
		return func() bool {
			hubs, err := adminReg.List(ctx)
			if err != nil {
				return false
			}
			for _, x := range hubs {
				if x.ID == hubID {
					return want(x)
				}
			}
			return false
		}
	}
	waitFor(t, "the hub's announcement", listed(func(x model.Hub) bool { return x.Accepting && !x.Draining }))

	// The agent registers (the hub's auth check over hub:requests and
	// hub:responses:<id>), streams (events on bus:events) and answers a
	// command routed to the hub's queue (its reply on bus:responses).
	t.Setenv("HUB_HOST", hubID)
	t.Setenv("HUB_PORT", port)
	agent := grpcagent.NewAgent(log, cfg, agentID)
	agent.SetDispatcher(okDispatcher{})
	agentCtx, cancelAgent := context.WithCancel(ctx)
	ran := make(chan error, 1)
	go func() { ran <- agent.Run(agentCtx) }()
	agentStopped := false
	stopAgent := func() {
		if agentStopped {
			return
		}
		agentStopped = true
		cancelAgent()
		<-ran
		stopCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = agent.Stop(stopCtx)
	}
	t.Cleanup(stopAgent)
	if ev := recvEvent(t, events, bus.EventServerOnline); ev.ServerID != agentID {
		t.Fatalf("server.online for %q", ev.ServerID)
	}
	n := 0
	waitFor(t, "a command through the hub", func() bool {
		n++
		id := fmt.Sprintf("acl-%d", n)
		return roundTrip(t, ctx, apiBus, responses, cfg.HubRequestQueue(hubID), agentID, id).Status == model.NotificationStatusSuccess
	})

	// The drain flag, set and cleared the way winterflow-hub drain /
	// undrain do (the hub's own connection): the hub follows it and
	// announces itself draining, then accepting again.
	if err := deps.Registry.SetDraining(ctx, hubID, true); err != nil {
		t.Fatalf("set drain flag: %v", err)
	}
	waitFor(t, "the hub announcing draining", listed(func(x model.Hub) bool { return x.Draining }))
	if err := deps.Registry.SetDraining(ctx, hubID, false); err != nil {
		t.Fatalf("clear drain flag: %v", err)
	}
	waitFor(t, "the hub accepting again", listed(func(x model.Hub) bool { return !x.Draining }))

	// Stopping with an agent on it: the drain reads the hub list (SCAN +
	// MGET of every hub's status). Other tests sharing this Redis may have
	// listed a hub, so the drain either finds none (ErrNoOtherHub) or tells
	// the agent to move and waits for it (the deadline). Its result says
	// nothing about the ACL (a list it cannot read counts as no other hub,
	// so a refused listing also ends in ErrNoOtherHub); the real assertions
	// are the direct List below, as the hub's user, and the empty ACL LOG
	// at the end.
	drainCtx, cancelDrain := context.WithTimeout(ctx, 2*time.Second)
	err = h.Drain(drainCtx)
	cancelDrain()
	if !errors.Is(err, hub.ErrNoOtherHub) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain = %v, want ErrNoOtherHub or the deadline", err)
	}
	peers, err := deps.Registry.List(ctx)
	if err != nil {
		t.Fatalf("hub list as the hub's user: %v", err)
	}
	found := false
	for _, x := range peers {
		found = found || x.ID == hubID
	}
	if !found {
		t.Fatalf("hub list as the hub's user misses the hub itself: %+v", peers)
	}
	// The stop: the announcer withdraws the entry.
	stopAgent()
	stopHub()
	waitFor(t, "the hub's entry withdrawn", func() bool {
		return admin.Exists(ctx, "hub:status:"+hubID).Val() == 0
	})

	entries, err := admin.ACLLog(ctx, 200).Result()
	if err != nil {
		t.Fatalf("acl log: %v", err)
	}
	for _, e := range entries {
		if e.Username == username {
			t.Errorf("refused for the hub's user: %s %s (%s)", e.Reason, e.Object, e.Context)
		}
	}
}
