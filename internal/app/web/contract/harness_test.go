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
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/agenthub"
	"winterflow/internal/domain/usecase/enrollment"
	"winterflow/internal/domain/usecase/enrollment/enrolltest"
	"winterflow/internal/domain/usecase/hubjoin"
	"winterflow/internal/domain/usecase/hubs"
	"winterflow/internal/domain/usecase/rollouts"
	"winterflow/internal/infra/bootstrap"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/dbtest"
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
	conn  *db.DB
	hubs  *hubList // the live hub list the agent hub route reads

	mu       sync.Mutex
	requests map[string]string // request_id -> "METHOD /path" that dispatched it
}

// newServerMu guards the go-pkgz/auth jwt package global. token.NewService
// writes jwt.MarshalSingleStringAsArray on every call (its sync.Once is
// function-local), while minting a token on login reads it through
// golang-jwt's ClaimStrings.MarshalJSON. Parallel tests therefore take the
// write lock around web.NewServer and a read lock around each /auth/ request
// (see serializeAuth). The only other reader is go-pkgz's refreshExpiredToken
// path, which re-mints a token that is past its 5-minute TokenDuration; no
// test env lives that long, so it never runs here. Production builds one
// server per process, so this is a test-only hazard.
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
	conn, err := db.Open(testLog, freshDB(t))
	if err != nil {
		t.Fatal(err)
	}
	// Parallel tests share one Postgres; keep each test's pool small.
	if conn.Driver == backend.Postgres {
		conn.SQL.SetMaxOpenConns(4)
	}
	t.Cleanup(func() { _ = conn.Shutdown() })

	ctx, cancel := context.WithCancel(context.Background())
	b := membus.NewBus(testLog)
	// The fake agent reports every 50 ms, so a rollout step's two healthy
	// reports need not be 20 s apart, nor polled every 5 s.
	deps, _ := bootstrap.WireCore(ctx, b, conn, cfg, testLog, bootstrap.WithRolloutTiming(rollouts.Timing{
		HealthSettle: 100 * time.Millisecond, HealthPoll: 50 * time.Millisecond,
	}))
	// Enrollment is distributed-only in production (BootstrapAPI); the
	// suite wires it here so its routes are exercised.
	deps.Enrollment = enrollment.New(deps.EnrollmentRepository, enrolltest.Signer(t), enrolltest.Picker{Hub: enrolltest.DefaultHub}, deps.HubCertRepository, nil)
	live := &hubList{hubs: []model.Hub{
		{ID: enrolltest.DefaultHub.ID, PublicAddr: enrolltest.DefaultHub.PublicAddr, Capacity: 100, Accepting: true},
		{ID: "hub-02.test", PublicAddr: "hub-02.test:443", Capacity: 100, Accepting: true},
	}}
	deps.HubJoin = hubjoin.New(deps.HubTokenRepository, deps.HubCertRepository, enrolltest.Signer(t), live, nil)
	deps.AgentHub = agenthub.New(deps.ServerRepository, live, hubs.NewPicker(live, deps.ServerRepository, testLog), deps.HubCertRepository, testLog)
	agent := newFakeAgent()
	if err := appagent.NewInProcessBridge(b, cfg, agent, testLog).Start(ctx); err != nil {
		t.Fatal(err)
	}
	// As BootstrapStandalone: the rollout worker starts once the bridge
	// consumes.
	bootstrap.StartRollouts(ctx, deps)

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

	e := &env{ctx: ctx, deps: deps, agent: agent, srv: srv, bus: b, cfg: cfg, conn: conn, hubs: live, requests: map[string]string{}}
	e.seedPendingServer(t)
	return e
}

// setStoredRole overwrites a user's organization_users.role, to stand in for
// an account created before roles were removed (stored role "member").
func (e *env) setStoredRole(t *testing.T, userID, role string) {
	t.Helper()
	dbtest.Exec(t, e.conn, `UPDATE organization_users SET role = ? WHERE user_id = ?`, role, userID)
}

// dbCtx bounds a test's direct repository calls: SQLite runs on one
// connection, so a deadlock must fail the test instead of hanging it. It
// still ends with e.ctx.
func (e *env) dbCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// seedPendingServer leaves one claimable registration, as BootstrapStandalone
// does at boot; the first login auto-claims it.
func (e *env) seedPendingServer(t *testing.T) string {
	t.Helper()
	ctx := e.dbCtx(t)
	code := util.GenerateRandomCode(6)
	err := e.deps.ServerRepository.RegisterServer(ctx, dto.ServerRegistrationDTO{
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
	ctx := e.dbCtx(t)
	u, err := e.deps.UserService.RegisterLocalUser(ctx, "Mallory", "mallory@other.test", "password")
	if err != nil {
		t.Fatalf("foreign user: %v", err)
	}
	orgID, err := e.deps.UserService.PrimaryOrganizationID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	code := e.seedPendingServer(t)
	srv, err := e.deps.ServerRepository.ClaimServer(ctx, dto.ClaimServerDTO{Code: code, OrganizationID: orgID})
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

// hubList is a settable stand-in for the Redis hub registry.
type hubList struct {
	mu   sync.Mutex
	hubs []model.Hub
}

func (l *hubList) Announce(context.Context, model.Hub, time.Duration) error { return nil }
func (l *hubList) Withdraw(context.Context, string) error                   { return nil }

func (l *hubList) List(context.Context) ([]model.Hub, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]model.Hub(nil), l.hubs...), nil
}

// setDraining marks the listed hub id draining.
func (l *hubList) setDraining(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.hubs {
		if l.hubs[i].ID == id {
			l.hubs[i].Draining = true
		}
	}
}
