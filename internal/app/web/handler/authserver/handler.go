// Package authserver is the HTTP edge of the OAuth authorization server MCP
// clients sign in to. The public endpoints (discovery metadata, dynamic
// client registration, the token endpoint) answer RFC 6749/7591/8414/9728
// JSON, not the API envelope, and allow any origin (browser-based MCP
// clients call them cross-origin, without credentials). The consent page's
// and the connected apps' routes are ordinary API routes behind the auth
// middleware. Every route answers 404 while the server is disabled.
//
// /oauth/authorize itself is not a Go route: it is the SPA's consent page,
// which reads its query and calls the consent routes.
package authserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/authserver"
	"winterflow/pkg/logger"
)

// Public paths.
const (
	ProtectedResourcePath    = "/.well-known/oauth-protected-resource"
	ProtectedResourceMCPPath = authserver.MetadataPath
	AuthServerMetadataPath   = "/.well-known/oauth-authorization-server"
	RegisterPath             = "/oauth/register"
	TokenPath                = "/oauth/token"
	// AuthorizePath is the SPA's consent page.
	AuthorizePath = "/oauth/authorize"
)

// maxBodyBytes bounds a registration or token request body.
const maxBodyBytes = 64 << 10

type Deps struct {
	Logger *logger.Logger
	// Server is the authorization server; nil (the spec-only route table)
	// reads as disabled.
	Server *authserver.UseCase
}

type Handler struct {
	log *logger.Logger
	srv *authserver.UseCase
}

func NewHandler(d *Deps) *Handler { return &Handler{log: d.Logger, srv: d.Server} }

// ProtectedResourceMetadata is /mcp's protected resource metadata (RFC 9728).
type ProtectedResourceMetadata struct {
	Resource               string   `json:"resource" required:"true"`
	AuthorizationServers   []string `json:"authorization_servers" required:"true"`
	ScopesSupported        []string `json:"scopes_supported" required:"true"`
	BearerMethodsSupported []string `json:"bearer_methods_supported" required:"true"`
}

// AuthServerMetadata is the authorization server metadata (RFC 8414).
type AuthServerMetadata struct {
	Issuer                            string   `json:"issuer" required:"true"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint" required:"true"`
	TokenEndpoint                     string   `json:"token_endpoint" required:"true"`
	RegistrationEndpoint              string   `json:"registration_endpoint" required:"true"`
	ResponseTypesSupported            []string `json:"response_types_supported" required:"true"`
	GrantTypesSupported               []string `json:"grant_types_supported" required:"true"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported" required:"true"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported" required:"true"`
	ScopesSupported                   []string `json:"scopes_supported" required:"true"`
}

// ClientRegistrationRequest is a dynamic client registration (RFC 7591).
type ClientRegistrationRequest struct {
	ClientName              string   `json:"client_name,omitempty"`
	RedirectURIs            []string `json:"redirect_uris" required:"true"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty" enum:"none,client_secret_post,client_secret_basic"`
}

// ClientRegistrationResponse is the registered client; client_secret (and its
// expiry, 0: never) only for a confidential client.
type ClientRegistrationResponse struct {
	ClientID                string   `json:"client_id" required:"true"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at" required:"true"`
	ClientName              string   `json:"client_name" required:"true"`
	RedirectURIs            []string `json:"redirect_uris" required:"true"`
	GrantTypes              []string `json:"grant_types" required:"true"`
	ResponseTypes           []string `json:"response_types" required:"true"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method" required:"true"`
	ClientSecret            string   `json:"client_secret,omitempty"`
	ClientSecretExpiresAt   *int64   `json:"client_secret_expires_at,omitempty"`
}

// TokenForm is the token endpoint's form body. Client credentials may come
// instead as HTTP Basic (client_secret_basic).
type TokenForm struct {
	GrantType    string `formData:"grant_type" required:"true" description:"authorization_code or refresh_token; anything else is unsupported_grant_type."`
	Code         string `formData:"code"`
	RedirectURI  string `formData:"redirect_uri"`
	CodeVerifier string `formData:"code_verifier"`
	RefreshToken string `formData:"refresh_token"`
	Resource     string `formData:"resource"`
	Scope        string `formData:"scope"`
	ClientID     string `formData:"client_id"`
	ClientSecret string `formData:"client_secret"`
}

// errorBody is an RFC 6749 error.
type errorBody struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

func (h *Handler) ProtectedResource(w http.ResponseWriter, _ *http.Request) {
	if !h.publicEnabled(w) {
		return
	}
	writeJSON(w, http.StatusOK, ProtectedResourceMetadata{
		Resource:               h.srv.ResourceURL(),
		AuthorizationServers:   []string{h.srv.Issuer()},
		ScopesSupported:        []string{authserver.Scope},
		BearerMethodsSupported: []string{"header"},
	})
}

func (h *Handler) Metadata(w http.ResponseWriter, _ *http.Request) {
	if !h.publicEnabled(w) {
		return
	}
	iss := h.srv.Issuer()
	writeJSON(w, http.StatusOK, AuthServerMetadata{
		Issuer:                            iss,
		AuthorizationEndpoint:             iss + AuthorizePath,
		TokenEndpoint:                     iss + TokenPath,
		RegistrationEndpoint:              iss + RegisterPath,
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none", "client_secret_post", "client_secret_basic"},
		ScopesSupported:                   []string{authserver.Scope},
	})
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	if !h.publicEnabled(w) {
		return
	}
	var req ClientRegistrationRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		h.writeOAuthError(w, &authserver.OAuthError{Status: http.StatusBadRequest, Code: "invalid_client_metadata", Description: "malformed JSON body"})
		return
	}
	res, err := h.srv.Register(r.Context(), model.ClientRegistration{
		ClientName: req.ClientName, RedirectURIs: req.RedirectURIs, GrantTypes: req.GrantTypes,
		ResponseTypes: req.ResponseTypes, TokenEndpointAuthMethod: req.TokenEndpointAuthMethod,
	})
	if err != nil {
		h.writeOAuthError(w, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusCreated, ClientRegistrationResponse{
		ClientID: res.ClientID, ClientIDIssuedAt: res.ClientIDIssuedAt, ClientName: res.ClientName,
		RedirectURIs: res.RedirectURIs, GrantTypes: res.GrantTypes, ResponseTypes: res.ResponseTypes,
		TokenEndpointAuthMethod: res.TokenEndpointAuthMethod, ClientSecret: res.ClientSecret,
		ClientSecretExpiresAt: res.ClientSecretExpiresAt,
	})
}

func (h *Handler) Token(w http.ResponseWriter, r *http.Request) {
	if !h.publicEnabled(w) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		h.writeOAuthError(w, &authserver.OAuthError{Status: http.StatusBadRequest, Code: "invalid_request", Description: "malformed form body"})
		return
	}
	// PostForm, not Form: credentials and grants must not be accepted from
	// the query string, where they would land in access logs.
	f := r.PostForm
	req := model.TokenRequest{
		GrantType: f.Get("grant_type"), Code: f.Get("code"), RedirectURI: f.Get("redirect_uri"),
		CodeVerifier: f.Get("code_verifier"), RefreshToken: f.Get("refresh_token"),
		Resource: f.Get("resource"), Scope: f.Get("scope"),
		ClientID: f.Get("client_id"), ClientSecret: f.Get("client_secret"),
	}
	if id, secret, ok := r.BasicAuth(); ok {
		// RFC 6749 2.3.1: both parts are form-urlencoded before the Basic
		// encoding.
		var err1, err2 error
		req.ClientID, err1 = url.QueryUnescape(id)
		req.ClientSecret, err2 = url.QueryUnescape(secret)
		if errors.Join(err1, err2) != nil {
			h.writeOAuthError(w, &authserver.OAuthError{Status: http.StatusUnauthorized, Code: "invalid_client", Description: "malformed client credentials"})
			return
		}
	}
	res, err := h.srv.Token(r.Context(), req)
	if err != nil {
		h.writeOAuthError(w, err)
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, res)
}

// publicEnabled answers the RFC 6749 404 of a disabled server.
func (h *Handler) publicEnabled(w http.ResponseWriter) bool {
	if h.srv.Enabled() {
		return true
	}
	writeJSON(w, http.StatusNotFound, errorBody{Error: "invalid_request", Description: "not available"})
	return false
}

// apiEnabled answers the API envelope's 404 of a disabled server.
func (h *Handler) apiEnabled(w http.ResponseWriter) bool {
	if h.srv.Enabled() {
		return true
	}
	webutil.ErrorStatus(w, http.StatusNotFound, model.ErrAuthServerDisabled.Error())
	return false
}

func (h *Handler) writeOAuthError(w http.ResponseWriter, err error) {
	noStore(w)
	var oe *authserver.OAuthError
	if !errors.As(err, &oe) {
		h.log.Error("authserver: endpoint failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "server_error"})
		return
	}
	if oe.Status == http.StatusUnauthorized && oe.Code == "invalid_client" {
		w.Header().Set("WWW-Authenticate", `Basic realm="winterflow"`)
	}
	writeJSON(w, oe.Status, errorBody{Error: oe.Code, Description: oe.Description})
}

// TooManyTokenRequests is the token endpoint's rate-limit answer: RFC 6749's
// JSON error (temporarily_unavailable, the code section 4.1.2.1 gives an
// overloaded server), not cacheable, readable from any origin like every
// other answer of the endpoint. The limiter has set Retry-After.
func TooManyTokenRequests(w http.ResponseWriter, _ *http.Request) {
	noStore(w)
	writeJSON(w, http.StatusTooManyRequests, errorBody{Error: "temporarily_unavailable",
		Description: "too many requests; retry after the Retry-After delay"})
}

// TooManyRegistrations is dynamic client registration's rate-limit answer,
// in RFC 7591's error shape (error, error_description).
func TooManyRegistrations(w http.ResponseWriter, _ *http.Request) {
	noStore(w)
	writeJSON(w, http.StatusTooManyRequests, errorBody{Error: "temporarily_unavailable",
		Description: "too many client registrations; retry after the Retry-After delay"})
}

// AllowAnyOrigin marks a public endpoint's answer readable from any origin:
// it carries no cookie-borne data, so credentials are never allowed.
func AllowAnyOrigin(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Del("Access-Control-Allow-Credentials")
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	AllowAnyOrigin(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// publicPaths are the endpoints MCP clients call cross-origin.
var publicPaths = map[string]bool{
	ProtectedResourcePath: true, ProtectedResourceMCPPath: true, AuthServerMetadataPath: true,
	RegisterPath: true, TokenPath: true,
}

// Preflight answers a CORS preflight of a public endpoint for any origin.
// It must run before the router's CORS middleware, which answers every
// preflight itself but allows only the configured origins.
func Preflight(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodOptions || !publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		AllowAnyOrigin(w)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
		w.Header().Set("Access-Control-Max-Age", "3600")
		w.WriteHeader(http.StatusNoContent)
	})
}
