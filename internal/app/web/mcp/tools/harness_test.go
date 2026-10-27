package tools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	appagent "winterflow/internal/app/agent"
	"winterflow/internal/app/web/api"
	webmcp "winterflow/internal/app/web/mcp"
	"winterflow/internal/app/web/mcp/tools"
	"winterflow/internal/app/web/middleware/ratelimit"
	"winterflow/internal/app/web/middleware/userlimit"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/port"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/internal/infra/bootstrap"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/transport/codec"
	"winterflow/internal/infra/transport/grpc/proto"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

var testLog = logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "mcp-tools"})

// reply is a scripted agent answer: Err makes it a failure.
type reply struct {
	Payload any
	Err     string
}

// fakeAgent stands in for the agent's Dispatcher behind the real
// InProcessBridge: each command type answers what its script says (a
// success with an empty object by default), after an optional delay.
type fakeAgent struct {
	mu      sync.Mutex
	scripts map[command.Type]func(json.RawMessage) reply
	delays  map[command.Type]time.Duration
	seen    []command.Type
}

func (a *fakeAgent) on(typ command.Type, f func(json.RawMessage) reply) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.scripts[typ] = f
}

func (a *fakeAgent) delay(typ command.Type, d time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.delays[typ] = d
}

// count is how many commands of typ the agent received.
func (a *fakeAgent) count(typ command.Type) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, s := range a.seen {
		if s == typ {
			n++
		}
	}
	return n
}

// total is how many commands the agent received.
func (a *fakeAgent) total() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.seen)
}

func (a *fakeAgent) Dispatch(ctx context.Context, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	typ := command.Type(req.Type)
	a.mu.Lock()
	a.seen = append(a.seen, typ)
	script, d := a.scripts[typ], a.delays[typ]
	a.mu.Unlock()
	if d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
		}
	}
	r := reply{Payload: map[string]any{}}
	if script != nil {
		r = script(req.Payload)
	}
	agentID := ""
	if req.Base != nil {
		agentID = req.Base.AgentId
	}
	code, detail, body := proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok", r.Payload
	if r.Err != "" {
		code, detail, body = proto.ResponseCode_RESPONSE_CODE_SERVER_ERROR, r.Err, nil
	}
	resp, err := codec.EncodeResponse(agentID, req.RequestId, typ, code, detail, body)
	if err != nil {
		panic(err)
	}
	return resp
}

// env is the standalone core (WireCore over an in-memory bus, the real
// InProcessBridge in front of a fake agent) with /mcp served the way
// routes.go serves it, tools included.
type env struct {
	ctx   context.Context
	deps  *bootstrap.Deps
	agent *fakeAgent
	url   string
}

// wait is the tools' wait bound in these tests: a command slower than it
// answers "running".
const wait = 1500 * time.Millisecond

// newEnv builds the stack; instances, when set, stands in for the template
// instance lookup.
func newEnv(t *testing.T, instances port.InstanceLinks) *env {
	t.Helper()
	cfg := config.NewServerConfig("standalone")
	conn := dbtest.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b := membus.NewBus(testLog)
	deps, _ := bootstrap.WireCore(ctx, b, conn, cfg, testLog)
	agent := &fakeAgent{scripts: map[command.Type]func(json.RawMessage) reply{}, delays: map[command.Type]time.Duration{}}
	agent.on(command.TypeAppSave, func(raw json.RawMessage) reply {
		var in command.SaveAppRequest
		_ = codec.DecodePayload(raw, &in)
		return reply{Payload: command.SaveAppResponse{AppID: in.App.AppID, Revision: "rev-1"}}
	})
	if err := appagent.NewInProcessBridge(b, cfg, agent, testLog).Start(ctx); err != nil {
		t.Fatal(err)
	}
	d := tools.FromDeps(deps)
	if instances != nil {
		d.Apps = appuc.NewUseCase(&appuc.Deps{
			CommandDispatcher: deps.CommandDispatcher, AppRepository: deps.AppRepository,
			AppDomainRepository: deps.AppDomainRepository, ServerDomainRepository: deps.ServerDomainRepository,
			ServerStorageRepository: deps.ServerStorageRepository, Servers: deps.ServerRepository,
			Instances: instances, Log: testLog,
		})
	}
	d.Wait = wait
	g := api.Guards{AuthLimit: ratelimit.New(100, 100).Middleware, UserLimit: userlimit.New(100, 100).Middleware}
	routes := webmcp.Routes(webmcp.Edge{Auth: deps.Authn, Register: tools.New(d)}, g)
	srv := httptest.NewServer(routes[0].Handler)
	t.Cleanup(srv.Close)
	return &env{ctx: ctx, deps: deps, agent: agent, url: srv.URL}
}

// user is an account with its own org, one server it owns and a PAT.
type user struct {
	id, orgID, serverID, token string
}

// newUser registers an account (its own org), claims a server into it and
// mints a PAT.
func (e *env) newUser(t *testing.T, email string) user {
	t.Helper()
	ctx := dbtest.Ctx(t)
	u, err := e.deps.UserService.RegisterLocalUser(ctx, "User", email, "password-123")
	if err != nil {
		t.Fatal(err)
	}
	orgID, err := e.deps.UserService.PrimaryOrganizationID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	code := util.GenerateRandomCode(6)
	err = e.deps.ServerRepository.RegisterServer(ctx, dto.ServerRegistrationDTO{
		ServerID: util.GenerateID(), CertificateID: util.GenerateID(), Hostname: "box-" + email,
		Code: code, ExpiresAt: time.Now().Add(time.Hour), Certificate: []byte("cert"),
		CertificateExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := e.deps.ServerRepository.ClaimServer(ctx, dto.ClaimServerDTO{Code: code, OrganizationID: orgID})
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := e.deps.Authn.CreatePAT(ctx, u.ID, "mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	return user{id: u.ID, orgID: orgID, serverID: srv.ID, token: token}
}

// bearer sends the token, and host (when set) as the Host header plus
// header's fields, on every request.
type bearer struct {
	token  string
	host   string
	header http.Header
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	if b.host != "" {
		r.Host = b.host
	}
	for k, v := range b.header {
		r.Header[k] = v
	}
	return http.DefaultTransport.RoundTrip(r)
}

// session is an MCP client session as u.
func (e *env) session(t *testing.T, u user) *sdk.ClientSession {
	t.Helper()
	return e.sessionVia(t, bearer{token: u.token})
}

// sessionVia is an MCP client session sending what rt says.
func (e *env) sessionVia(t *testing.T, rt bearer) *sdk.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()
	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	s, err := c.Connect(ctx, &sdk.StreamableClientTransport{
		Endpoint: e.url, HTTPClient: &http.Client{Transport: rt}, MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// call runs a tool and returns its result's text and isError.
func call(t *testing.T, s *sdk.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := s.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("%s: content = %+v", name, res.Content)
	}
	return res.Content[0].(*sdk.TextContent).Text, res.IsError
}

// ok runs a tool that must succeed and decodes its JSON answer.
func ok(t *testing.T, s *sdk.ClientSession, name string, args map[string]any, v any) {
	t.Helper()
	text, isErr := call(t, s, name, args)
	if isErr {
		t.Fatalf("%s: error %s", name, text)
	}
	if err := json.Unmarshal([]byte(text), v); err != nil {
		t.Fatalf("%s: decode %s: %v", name, text, err)
	}
}
