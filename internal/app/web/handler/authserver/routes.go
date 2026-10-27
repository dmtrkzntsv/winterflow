package authserver

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
)

// Routes is the authorization server's slice of the route table. The
// public endpoints are Raw (RFC JSON, any origin); registration stores a
// row per call and sits behind g.OAuthRegisterLimit, the token endpoint
// behind g.AuthLimit.
func (h *Handler) Routes(g api.Guards) []api.Route {
	off := []int{http.StatusNotFound}
	return []api.Route{
		{
			Method: http.MethodGet, Path: ProtectedResourcePath, Handler: h.ProtectedResource, Auth: api.Public, Raw: true,
			Summary: "OAuth protected resource metadata (RFC 9728) of /mcp", Tag: "oauth",
			Response: ProtectedResourceMetadata{}, Errors: off,
		},
		{
			Method: http.MethodGet, Path: ProtectedResourceMCPPath, Handler: h.ProtectedResource, Auth: api.Public, Raw: true,
			Summary: "OAuth protected resource metadata (RFC 9728) of /mcp, at its path-specific location", Tag: "oauth",
			Response: ProtectedResourceMetadata{}, Errors: off,
		},
		{
			Method: http.MethodGet, Path: AuthServerMetadataPath, Handler: h.Metadata, Auth: api.Public, Raw: true,
			Summary: "OAuth authorization server metadata (RFC 8414)", Tag: "oauth",
			Description: "The server runs only while WEB_URL is an https origin with no path (or http on loopback); " +
				"otherwise every OAuth route answers 404.",
			Response: AuthServerMetadata{}, Errors: off,
		},
		{
			Method: http.MethodPost, Path: RegisterPath, Handler: h.Register, Auth: api.Public, Raw: true,
			Middleware: []api.Middleware{g.OAuthRegisterLimit}, Status: http.StatusCreated,
			Summary: "Register an OAuth client (RFC 7591 dynamic client registration)", Tag: "oauth",
			Description: "Redirect URIs must be https, or http on a loopback host (whose port may differ at sign-in). " +
				"A client_secret_* method returns a client_secret, shown once.",
			Request: ClientRegistrationRequest{}, Response: ClientRegistrationResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError},
		},
		{
			Method: http.MethodPost, Path: TokenPath, Handler: h.Token, Auth: api.Public, Raw: true,
			Middleware: []api.Middleware{g.AuthLimit},
			Summary:    "OAuth token endpoint: authorization_code (PKCE S256) and refresh_token grants", Tag: "oauth",
			Description: "Access tokens (wfo_) last an hour and work on /mcp only; refresh tokens (wfr_) rotate on every use. " +
				"Replaying a rotated refresh token outside a 60-second grace window revokes the whole grant.",
			Form: TokenForm{}, Response: model.TokenResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound,
				http.StatusTooManyRequests, http.StatusInternalServerError},
		},
		{
			Method: http.MethodGet, Path: "/api/v1/authserver/get-authorization-request", Handler: h.GetAuthorizationRequest, Auth: api.User,
			Summary: "Describe an OAuth authorization request for the consent page", Tag: "oauth",
			Description: "400 when the client or its redirect cannot be trusted (show the message); a bad OAuth parameter " +
				"with a trusted redirect answers 200 with `error_redirect_url`, where the page sends the browser.",
			Query: AuthorizationQuery{}, Response: AuthorizationDescription{},
			Errors: []int{http.StatusBadRequest, http.StatusNotFound},
		},
		{
			Method: http.MethodPost, Path: "/api/v1/authserver/approve-authorization", Handler: h.ApproveAuthorization, Auth: api.User,
			Summary: "Approve an OAuth authorization: issues a one-time code", Tag: "oauth",
			Description: "The client gets full access to the caller's organization over /mcp. Navigate to `redirect_url`.",
			Request:     AuthorizationRequest{}, Response: AuthorizationDecision{},
			Errors: []int{http.StatusBadRequest, http.StatusNotFound},
		},
		{
			Method: http.MethodPost, Path: "/api/v1/authserver/decline-authorization", Handler: h.DeclineAuthorization, Auth: api.User,
			Summary: "Decline an OAuth authorization", Tag: "oauth",
			Description: "Navigate to `redirect_url`, which sends access_denied back to the client.",
			Request:     AuthorizationRequest{}, Response: AuthorizationDecision{},
			Errors: []int{http.StatusBadRequest, http.StatusNotFound},
		},
		{
			Method: http.MethodGet, Path: "/api/v1/user/get-connected-apps", Handler: h.GetConnectedApps, Auth: api.User,
			Summary: "List the caller's connected apps (OAuth grants of MCP clients)", Tag: "oauth",
			Response: []ConnectedApp{}, Errors: []int{http.StatusBadRequest, http.StatusNotFound},
		},
		{
			Method: http.MethodPost, Path: "/api/v1/user/revoke-connected-app", Handler: h.RevokeConnectedApp, Auth: api.User,
			Summary: "Revoke a connected app: its tokens stop working at once", Tag: "oauth",
			Request: RevokeConnectedAppRequest{},
			Errors:  []int{http.StatusBadRequest, http.StatusNotFound},
		},
	}
}
