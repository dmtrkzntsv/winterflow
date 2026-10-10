package contract_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	appagent "winterflow/internal/app/agent"
	"winterflow/internal/app/web"
	"winterflow/internal/domain/dto"
	"winterflow/internal/infra/bootstrap"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/transport/bus"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

// env is one isolated stack: fresh DB, the standalone core wiring, the real
// InProcessBridge in front of a fake agent, and the real router behind an
// httptest server.
type env struct {
	ctx   context.Context
	deps  *bootstrap.Deps
	agent *fakeAgent
	srv   *httptest.Server
	bus   *membus.Bus
	cfg   *config.ServerConfig

	mu       sync.Mutex
	requests map[string]string // request_id -> "METHOD /path" that dispatched it
}

// newServerMu guards the go-pkgz/auth jwt package global. token.NewService
// writes jwt.MarshalSingleStringAsArray on every call (its sync.Once is
// function-local), while minting a token on login reads it through
// golang-jwt's ClaimStrings.MarshalJSON. Parallel tests therefore take the
// write lock around web.NewServer and a read lock around each /auth/ request
// (see serializeAuth). Production builds one server per process, so this is a
// test-only hazard.
var newServerMu sync.RWMutex

// serializeAuth holds newServerMu's read lock for the duration of every
// /auth/ request, the only ones that mint tokens, so no server build can
// overlap a login. Long-lived requests (SSE) are deliberately not locked.
func serializeAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			newServerMu.RLock()
			defer newServerMu.RUnlock()
		}
		next.ServeHTTP(w, r)
	})
}

var testLog = logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "contract"})

// newEnv builds the stack the way BootstrapStandalone does (WireCore over an
// in-memory bus + InProcessBridge), minus Docker, certs and ingress.
func newEnv(t *testing.T) *env {
	t.Helper()
	cfg := config.NewServerConfig("standalone")
	conn := db.NewBunConnection(testLog, freshDB(t))
	// Parallel tests share one Postgres; keep each test's pool small.
	conn.GetDB().SetMaxOpenConns(4)
	t.Cleanup(func() { _ = conn.Shutdown() })

	ctx, cancel := context.WithCancel(context.Background())
	b := membus.NewBus(testLog)
	deps, _ := bootstrap.WireCore(ctx, b, conn, cfg, testLog)
	agent := newFakeAgent()
	if err := appagent.NewInProcessBridge(b, cfg, agent, testLog).Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Exclusive: no /auth/ request in any env may run while NewServer writes
	// the jwt global (see newServerMu).
	newServerMu.Lock()
	handler := web.NewServer(ctx, deps).Handler
	newServerMu.Unlock()
	srv := httptest.NewUnstartedServer(serializeAuth(record(handler)))
	// SSE handlers observe ctx through BaseContext: cancelling it ends
	// streams so Close does not wait on them.
	srv.Config.BaseContext = func(_ net.Listener) context.Context { return ctx }
	srv.Start()
	t.Cleanup(srv.Close)
	t.Cleanup(cancel) // runs before srv.Close (LIFO)

	e := &env{ctx: ctx, deps: deps, agent: agent, srv: srv, bus: b, cfg: cfg, requests: map[string]string{}}
	e.seedPendingServer(t)
	return e
}

// seedPendingServer leaves one claimable registration, as BootstrapStandalone
// does at boot; the first login auto-claims it.
func (e *env) seedPendingServer(t *testing.T) string {
	t.Helper()
	code := util.GenerateRandomCode(6)
	err := e.deps.ServerRepository.RegisterServer(e.ctx, dto.ServerRegistrationDTO{
		ServerID: util.GenerateID(), CertificateID: util.GenerateID(), Hostname: "contract",
		Code: code, ExpiresAt: time.Now().Add(time.Hour), Certificate: []byte("cert"),
		CertificateExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("seed pending server: %v", err)
	}
	return code
}

// foreignServer creates a second org (own user) with a claimed server and
// returns that server's id: the 403 probe for server-addressed routes.
func (e *env) foreignServer(t *testing.T) string {
	t.Helper()
	u, err := e.deps.UserService.RegisterLocalUser(e.ctx, "Mallory", "mallory@other.test", "password")
	if err != nil {
		t.Fatalf("foreign user: %v", err)
	}
	orgID, err := e.deps.UserService.PrimaryOrganizationID(e.ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	code := e.seedPendingServer(t)
	srv, err := e.deps.ServerRepository.ClaimServer(e.ctx, dto.ClaimServerDTO{Code: code, OrganizationID: orgID})
	if err != nil {
		t.Fatalf("foreign claim: %v", err)
	}
	return srv.ID
}

func (e *env) rememberRequest(id, route string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.requests[id] = route
}

func (e *env) routeOfRequest(id string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.requests[id]
}

// publishEvent puts an agent-initiated event on the events queue, the way
// the Hub (or standalone's status reporter) does.
func (e *env) publishEvent(t *testing.T, ev bus.EventMessage) {
	t.Helper()
	if err := e.bus.Publish(e.ctx, e.cfg.GetBusEventsQueue(), ev); err != nil {
		t.Fatal(err)
	}
}

// eventually polls cond for up to resultTimeout (the events queue is async).
func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(resultTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within " + resultTimeout.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
