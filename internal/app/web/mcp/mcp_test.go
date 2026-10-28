package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"winterflow/internal/app/web/api"
	webmcp "winterflow/internal/app/web/mcp"
	"winterflow/internal/app/web/middleware/ratelimit"
	"winterflow/internal/app/web/middleware/userlimit"
	"winterflow/internal/domain/model"
)

// fakeAuth knows one token per kind.
type fakeAuth map[string]model.Principal

func (f fakeAuth) Authenticate(_ context.Context, raw string) (model.Principal, error) {
	p, ok := f[raw]
	if !ok {
		return model.Principal{}, model.ErrUnauthorized
	}
	return p, nil
}

var tokens = fakeAuth{
	"wfp_alice": {UserID: "alice", TokenID: "t1", Kind: model.TokenPAT, Scope: model.ScopeFull},
	"wfp_bob":   {UserID: "bob", TokenID: "t2", Kind: model.TokenPAT, Scope: model.ScopeFull},
	"wfo_alice": {UserID: "alice", TokenID: "t3", Kind: model.TokenOAuth, Scope: model.ScopeMCP},
}

const challenge = `Bearer resource_metadata="https://wf.example/.well-known/oauth-protected-resource/mcp", scope="mcp"`

// whoami is the one tool the edge tests register: it answers the caller.
func whoami(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: "whoami", Description: "The caller's user id."},
		func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
			return webmcp.JSONResult(map[string]string{"user_id": webmcp.UserID(ctx)}), nil, nil
		})
}

// serve mounts /mcp the way routes.go does, with fresh limiters.
func serve(t *testing.T, allowOAuth bool) *httptest.Server {
	t.Helper()
	g := api.Guards{AuthLimit: ratelimit.New(5, 10).Middleware, UserLimit: userlimit.New(10, 30).Middleware}
	routes := webmcp.Routes(webmcp.Edge{Auth: tokens, AllowOAuth: allowOAuth, Challenge: challenge, Register: whoami}, g)
	if len(routes) != 1 || routes[0].Handler == nil {
		t.Fatalf("routes = %+v", routes)
	}
	srv := httptest.NewServer(routes[0].Handler)
	t.Cleanup(srv.Close)
	return srv
}

// bearerTransport adds a bearer token to every request.
type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// connect opens an MCP session as token's user.
func connect(t *testing.T, url, token string) *sdk.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	sess, err := c.Connect(ctx, &sdk.StreamableClientTransport{
		Endpoint: url, HTTPClient: &http.Client{Transport: bearerTransport{token}}, MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func callWhoami(t *testing.T, sess *sdk.ClientSession) string {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{Name: "whoami"})
	if err != nil || res.IsError {
		t.Fatalf("whoami = %+v, %v", res, err)
	}
	return res.Content[0].(*sdk.TextContent).Text
}

// post sends one raw JSON-RPC ping.
func post(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp
}

func TestMCPUnauthenticatedChallenge(t *testing.T) {
	srv := serve(t, true)
	for _, token := range []string{"", "wfp_unknown"} {
		resp := post(t, srv.URL, token)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token %q: status %d, want 401", token, resp.StatusCode)
		}
		if got := resp.Header.Get("WWW-Authenticate"); got != challenge {
			t.Fatalf("WWW-Authenticate = %q, want %q", got, challenge)
		}
	}
	// Refusals pass the per-IP limiter (burst 10): two used above.
	for i := 0; i < 8; i++ {
		post(t, srv.URL, "")
	}
	if got := post(t, srv.URL, "").StatusCode; got != http.StatusTooManyRequests {
		t.Fatalf("11th refusal = %d, want 429", got)
	}
	// An authenticated call does not draw on that bucket.
	if got := post(t, srv.URL, "wfp_alice").StatusCode; got != http.StatusOK {
		t.Fatalf("authenticated call after the refusals = %d, want 200", got)
	}
}

func TestMCPWithPAT(t *testing.T) {
	srv := serve(t, true)
	sess := connect(t, srv.URL, "wfp_alice")
	tools, err := sess.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "whoami" {
		t.Fatalf("tools/list = %+v, %v", tools, err)
	}
	if got := callWhoami(t, sess); got != `{"user_id":"alice"}` {
		t.Fatalf("whoami = %s", got)
	}
	if res := sess.InitializeResult(); res.ServerInfo.Name != "winterflow" || !strings.Contains(res.Instructions, "request_secrets") {
		t.Fatalf("initialize = %+v", res)
	}
}

func TestMCPWithOAuthToken(t *testing.T) {
	sess := connect(t, serve(t, true).URL, "wfo_alice")
	if got := callWhoami(t, sess); got != `{"user_id":"alice"}` {
		t.Fatalf("whoami = %s", got)
	}
	// With the authorization server off, OAuth tokens are refused.
	if got := post(t, serve(t, false).URL, "wfo_alice").StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("OAuth token with the server off = %d, want 401", got)
	}
}

func TestMCPRateLimitedPerUser(t *testing.T) {
	srv := serve(t, true)
	for i := 0; i < 30; i++ {
		if got := post(t, srv.URL, "wfp_alice").StatusCode; got != http.StatusOK {
			t.Fatalf("call %d = %d, want 200", i+1, got)
		}
	}
	if got := post(t, srv.URL, "wfp_alice").StatusCode; got != http.StatusTooManyRequests {
		t.Fatalf("31st immediate call = %d, want 429", got)
	}
	if got := post(t, srv.URL, "wfp_bob").StatusCode; got != http.StatusOK {
		t.Fatalf("another user = %d, want 200", got)
	}
}

// postBody sends body as alice, with a known length or chunked.
func postBody(t *testing.T, url string, body []byte, chunked bool) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader = bytes.NewReader(body)
	if chunked {
		r = io.MultiReader(r) // hides the length: sent chunked
	}
	req, _ := http.NewRequest(http.MethodPost, url, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer wfp_alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func TestMCPBodyLimit(t *testing.T) {
	srv := serve(t, true)
	// A ping padded past the bound (a JSON-RPC message may carry params).
	pad := strings.Repeat("a", webmcp.MaxBodyBytes)
	big := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"_meta":{"pad":"` + pad + `"}}}`)
	for _, chunked := range []bool{false, true} {
		resp, out := postBody(t, srv.URL, big, chunked)
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("chunked=%v: status %d, want 413 (%s)", chunked, resp.StatusCode, out)
		}
		var rpc struct {
			JSONRPC string `json:"jsonrpc"`
			Error   struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("chunked=%v: Content-Type %q", chunked, ct)
		}
		if err := json.Unmarshal(out, &rpc); err != nil || rpc.JSONRPC != "2.0" || rpc.Error.Code != -32600 ||
			!strings.Contains(rpc.Error.Message, "exceeds") {
			t.Fatalf("chunked=%v: body %s (%v)", chunked, out, err)
		}
	}
	// A body above the SDK's 4 MiB default but within the bound is served.
	mid := strings.Repeat("a", 5<<20)
	ok := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"_meta":{"pad":"` + mid + `"}}}`)
	for _, chunked := range []bool{false, true} {
		if resp, out := postBody(t, srv.URL, ok, chunked); resp.StatusCode != http.StatusOK {
			t.Fatalf("chunked=%v: 5 MiB body = %d (%s), want 200", chunked, resp.StatusCode, out)
		}
	}
}
