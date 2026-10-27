package contract_test

import (
	"net/http"
	"strings"
	"testing"
)

// mcpCall is one JSON-RPC message to /mcp as an MCP client sends it.
func mcpCall(body string) call {
	return call{Method: http.MethodPost, Path: "/mcp", Body: body,
		Header: http.Header{"Accept": {"application/json, text/event-stream"}}}
}

const mcpInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",` +
	`"capabilities":{},"clientInfo":{"name":"contract","version":"1"}}}`

// TestMCPEndpoint: /mcp answers a personal access token and an OAuth access
// token, and refuses a call without one with the OAuth challenge.
func TestMCPEndpoint(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)

	r := owner.pat(t).do(t, mcpCall(mcpInitialize)).expect(t, http.StatusOK)
	if !strings.Contains(string(r.Body), `"name":"winterflow"`) {
		t.Fatalf("initialize = %s", r.Body)
	}

	_, tok := connectMCP(t, owner)
	oauth := e.newClient()
	oauth.bearer = tok.AccessToken
	oauth.do(t, mcpCall(mcpInitialize)).expect(t, http.StatusOK)

	r = e.anon().do(t, mcpCall(mcpInitialize)).expect(t, http.StatusUnauthorized)
	want := `Bearer resource_metadata="` + issuer + `/.well-known/oauth-protected-resource/mcp", scope="mcp"`
	if got := r.Header.Get("WWW-Authenticate"); got != want {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
	}
}
