package mcp

import (
	"encoding/json"
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/app/web/middleware/authn"
)

// Path is the MCP endpoint.
const Path = "/mcp"

// Edge is what /mcp is served with.
type Edge struct {
	// Auth resolves the caller's token (the authn usecase).
	Auth authn.Authenticator
	// AllowOAuth admits OAuth access tokens: only while the authorization
	// server runs.
	AllowOAuth bool
	// Challenge is the WWW-Authenticate value of a 401 ("" while the
	// authorization server is off).
	Challenge string
	// Register adds the tools.
	Register Register
}

// JSONRPCRequest documents a JSON-RPC 2.0 request (one MCP message).
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse documents a JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

// Routes is /mcp's slice of the route table. The route is Public to the
// table: its handler authenticates itself, so the refusal can carry the
// OAuth challenge and pass g.AuthLimit (per IP), while an authenticated
// call passes g.UserLimit (per user) instead. Without the guards (the
// spec-only table) the handler is nil, which Register refuses.
func Routes(e Edge, g api.Guards) []api.Route {
	var h http.HandlerFunc
	if e.Register != nil && g.AuthLimit != nil && g.UserLimit != nil {
		authed := authn.Middleware(e.Auth, authn.Options{
			AllowOAuth: e.AllowOAuth, Challenge: e.Challenge, DenyLimit: g.AuthLimit, RawErrors: true,
		})
		h = authed(g.UserLimit(NewHandler(e.Register))).ServeHTTP
	}
	return []api.Route{{
		Method: http.MethodPost, Path: Path, Handler: h, Auth: api.Public, Raw: true,
		Summary: "MCP endpoint (Streamable HTTP, stateless, JSON responses)", Tag: "mcp",
		Description: "Model Context Protocol over Streamable HTTP: each POST carries one JSON-RPC message and is " +
			"answered with one JSON body. Authenticate with a personal access token (`Authorization: Bearer wfp_...`), " +
			"the session cookie, or an OAuth access token issued to an MCP client while the authorization server runs; " +
			"a call without a valid credential answers 401 with `WWW-Authenticate: Bearer resource_metadata=..., " +
			"scope=\"mcp\"` when it does. Calls are limited per user (about 10 a second, burst 30), refusals per IP. " +
			"See docs/mcp-setup.md.",
		Request: JSONRPCRequest{}, Response: JSONRPCResponse{},
		Errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests},
	}}
}
