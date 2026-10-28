package contract_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"winterflow/internal/infra/bootstrap"
)

const (
	mcpCallback  = "https://claude.ai/api/mcp/auth_callback"
	pkceVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	// pkceChallenge is pkceVerifier's S256 challenge (RFC 7636 appendix B).
	pkceChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	// issuer is the suite's WEB_URL origin (TestMain).
	issuer = "http://127.0.0.1"
)

// oauthJSON decodes a Raw (non-envelope) answer.
func oauthJSON(t *testing.T, r *response, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}
}

// registerMCPClient registers a public MCP client and returns its id.
func registerMCPClient(t *testing.T, c *client) string {
	t.Helper()
	var reg struct {
		ClientID string `json:"client_id"`
	}
	oauthJSON(t, c.post(t, "/oauth/register", map[string]any{
		"client_name": "Claude", "redirect_uris": []string{mcpCallback},
	}).expect(t, http.StatusCreated), &reg)
	return reg.ClientID
}

func authorizeQuery(clientID string) url.Values {
	return url.Values{
		"client_id": {clientID}, "redirect_uri": {mcpCallback}, "response_type": {"code"},
		"code_challenge": {pkceChallenge}, "code_challenge_method": {"S256"},
		"resource": {issuer + "/mcp"}, "state": {"s1"},
	}
}

func authorizeBody(clientID string) map[string]string {
	b := map[string]string{}
	for k, v := range authorizeQuery(clientID) {
		b[k] = v[0]
	}
	return b
}

// tokenCall posts form to the token endpoint.
func (c *client) tokenCall(t *testing.T, form url.Values) *response {
	t.Helper()
	return c.do(t, call{Method: http.MethodPost, Path: "/oauth/token", Body: form.Encode(),
		Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}})
}

type tokenAnswer struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

// connectMCP runs a whole connection as c: registration, consent approval,
// code exchange. It returns the client id and the tokens.
func connectMCP(t *testing.T, c *client) (string, tokenAnswer) {
	t.Helper()
	anon := c.e.anon()
	clientID := registerMCPClient(t, anon)
	var d struct {
		RedirectURL string `json:"redirect_url"`
	}
	c.post(t, "/api/v1/authserver/approve-authorization", authorizeBody(clientID)).expect(t, http.StatusOK).data(t, &d)
	to, err := url.Parse(d.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(d.RedirectURL, mcpCallback+"?") || to.Query().Get("state") != "s1" || to.Query().Get("iss") != issuer {
		t.Fatalf("approve redirect = %s", d.RedirectURL)
	}
	var tok tokenAnswer
	oauthJSON(t, anon.tokenCall(t, url.Values{
		"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {to.Query().Get("code")},
		"redirect_uri": {mcpCallback}, "code_verifier": {pkceVerifier}, "resource": {issuer + "/mcp"},
	}).expect(t, http.StatusOK), &tok)
	if !strings.HasPrefix(tok.AccessToken, "wfo_") || !strings.HasPrefix(tok.RefreshToken, "wfr_") || tok.Scope != "mcp" {
		t.Fatalf("token answer = %+v", tok)
	}
	return clientID, tok
}

type connectedApp struct {
	GrantID      string `json:"grant_id"`
	ClientName   string `json:"client_name"`
	RedirectHost string `json:"redirect_host"`
	IsLoopback   bool   `json:"is_loopback"`
}

func (c *client) connectedApps(t *testing.T) []connectedApp {
	t.Helper()
	var apps []connectedApp
	c.get(t, "/api/v1/user/get-connected-apps", nil).expect(t, http.StatusOK).data(t, &apps)
	return apps
}

// TestOAuthDiscovery: the metadata documents name this origin; their
// preflights allow any origin.
func TestOAuthDiscovery(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.anon()
	var as struct {
		Issuer                string   `json:"issuer"`
		AuthorizationEndpoint string   `json:"authorization_endpoint"`
		TokenEndpoint         string   `json:"token_endpoint"`
		RegistrationEndpoint  string   `json:"registration_endpoint"`
		Methods               []string `json:"code_challenge_methods_supported"`
	}
	oauthJSON(t, c.get(t, "/.well-known/oauth-authorization-server", nil).expect(t, http.StatusOK), &as)
	if as.Issuer != issuer || as.AuthorizationEndpoint != issuer+"/oauth/authorize" || as.TokenEndpoint != issuer+"/oauth/token" ||
		as.RegistrationEndpoint != issuer+"/oauth/register" || len(as.Methods) != 1 || as.Methods[0] != "S256" {
		t.Fatalf("authorization server metadata = %+v", as)
	}
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		var pr struct {
			Resource string   `json:"resource"`
			Servers  []string `json:"authorization_servers"`
		}
		r := c.get(t, p, nil).expect(t, http.StatusOK)
		oauthJSON(t, r, &pr)
		if pr.Resource != issuer+"/mcp" || len(pr.Servers) != 1 || pr.Servers[0] != issuer {
			t.Fatalf("%s = %+v", p, pr)
		}
		if r.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("%s allows origin %q", p, r.Header.Get("Access-Control-Allow-Origin"))
		}
	}
	// A preflight is not in the route table (the spec documents no
	// OPTIONS), so it goes around the validating client.
	req, err := http.NewRequestWithContext(e.ctx, http.MethodOptions, e.srv.URL+"/oauth/token", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://inspector.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	pre, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = pre.Body.Close()
	if pre.StatusCode != http.StatusNoContent || pre.Header.Get("Access-Control-Allow-Origin") != "*" ||
		pre.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("preflight: %d %v", pre.StatusCode, pre.Header)
	}
	var st struct {
		OAuthServer bool `json:"oauth_server"`
	}
	c.get(t, "/api/v1/auth/state", nil).expect(t, http.StatusOK).data(t, &st)
	if !st.OAuthServer {
		t.Fatal("auth state must report the OAuth server")
	}
}

// TestOAuthConnectFlow: register, describe, approve, exchange, refresh,
// list; decline sends access_denied; a bad registration is an RFC 7591
// error.
func TestOAuthConnectFlow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)

	clientID := registerMCPClient(t, e.anon())
	var desc struct {
		ClientName       string `json:"client_name"`
		RedirectHost     string `json:"redirect_host"`
		ErrorRedirectURL string `json:"error_redirect_url"`
	}
	owner.get(t, "/api/v1/authserver/get-authorization-request", authorizeQuery(clientID)).expect(t, http.StatusOK).data(t, &desc)
	if desc.ClientName != "Claude" || desc.RedirectHost != "claude.ai" || desc.ErrorRedirectURL != "" {
		t.Fatalf("description = %+v", desc)
	}
	bad := authorizeQuery(clientID)
	bad.Set("resource", "https://other.example/mcp")
	owner.get(t, "/api/v1/authserver/get-authorization-request", bad).expect(t, http.StatusOK).data(t, &desc)
	if !strings.Contains(desc.ErrorRedirectURL, "error=invalid_target") {
		t.Fatalf("a wrong resource must redirect invalid_target: %+v", desc)
	}
	var dec struct {
		RedirectURL string `json:"redirect_url"`
	}
	owner.post(t, "/api/v1/authserver/decline-authorization", authorizeBody(clientID)).expect(t, http.StatusOK).data(t, &dec)
	if !strings.Contains(dec.RedirectURL, "error=access_denied") {
		t.Fatalf("decline redirect = %s", dec.RedirectURL)
	}

	cid, tok := connectMCP(t, owner)
	// OAuth tokens are for /mcp only.
	e.anon().do(t, call{Method: http.MethodGet, Path: "/api/v1/user/get-profile",
		Header: http.Header{"Authorization": {"Bearer " + tok.AccessToken}}}).expect(t, http.StatusUnauthorized)
	var next tokenAnswer
	oauthJSON(t, e.anon().tokenCall(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {cid},
		"refresh_token": {tok.RefreshToken}}).expect(t, http.StatusOK), &next)
	if next.RefreshToken == tok.RefreshToken || !strings.HasPrefix(next.AccessToken, "wfo_") {
		t.Fatalf("refresh = %+v", next)
	}
	apps := owner.connectedApps(t)
	if len(apps) != 1 || apps[0].ClientName != "Claude" || apps[0].RedirectHost != "claude.ai" || apps[0].IsLoopback {
		t.Fatalf("connected apps = %+v", apps)
	}

	var oe struct {
		Error string `json:"error"`
	}
	oauthJSON(t, e.anon().post(t, "/oauth/register", map[string]any{"redirect_uris": []string{"http://evil.example/cb"}}).
		expect(t, http.StatusBadRequest), &oe)
	if oe.Error != "invalid_redirect_uri" {
		t.Fatalf("bad registration error = %q", oe.Error)
	}
	oauthJSON(t, e.anon().tokenCall(t, url.Values{"grant_type": {"password"}}).expect(t, http.StatusBadRequest), &oe)
	if oe.Error != "unsupported_grant_type" {
		t.Fatalf("bad grant error = %q", oe.Error)
	}
}

// TestRevokeConnectedAppCutsAccess: revoking a connected app stops its
// access token at once, and its refresh token with it.
func TestRevokeConnectedAppCutsAccess(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	cid, tok := connectMCP(t, owner)
	if _, err := e.deps.Authn.Authenticate(e.ctx, tok.AccessToken); err != nil {
		t.Fatalf("the access token must authenticate before the revoke: %v", err)
	}
	apps := owner.connectedApps(t)
	if len(apps) != 1 {
		t.Fatalf("connected apps = %+v", apps)
	}
	owner.post(t, "/api/v1/user/revoke-connected-app", map[string]string{"grant_id": apps[0].GrantID}).expect(t, http.StatusOK)
	if _, err := e.deps.Authn.Authenticate(e.ctx, tok.AccessToken); err == nil {
		t.Fatal("a revoked connected app's access token still authenticates")
	}
	var oe struct {
		Error string `json:"error"`
	}
	oauthJSON(t, e.anon().tokenCall(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {cid},
		"refresh_token": {tok.RefreshToken}}).expect(t, http.StatusBadRequest), &oe)
	if oe.Error != "invalid_grant" {
		t.Fatalf("refresh after the revoke: %q", oe.Error)
	}
	if apps := owner.connectedApps(t); len(apps) != 0 {
		t.Fatalf("still listed: %+v", apps)
	}
	owner.post(t, "/api/v1/user/revoke-connected-app", map[string]string{"grant_id": apps[0].GrantID}).expect(t, http.StatusNotFound)
}

// TestRemoveMemberRevokesGrants: removing a member cuts their connected
// apps.
func TestRemoveMemberRevokesGrants(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	m := owner.member(t)
	cid, tok := connectMCP(t, m)
	owner.post(t, "/api/v1/org/remove-member", map[string]string{"user_id": m.userID}).expect(t, http.StatusOK)
	if _, err := e.deps.Authn.Authenticate(e.ctx, tok.AccessToken); err == nil {
		t.Fatal("a removed member's OAuth access token still authenticates")
	}
	e.anon().tokenCall(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {cid},
		"refresh_token": {tok.RefreshToken}}).expect(t, http.StatusBadRequest)
}

// TestDisabledServer404: without a usable WEB_URL origin every OAuth route
// answers 404 and the auth state says so.
func TestDisabledServer404(t *testing.T) {
	t.Parallel()
	e := newEnv(t, bootstrap.WithAuthServerIssuer(""))
	owner := e.claim(t)
	anon := e.anon()
	for _, p := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp"} {
		anon.get(t, p, nil).expect(t, http.StatusNotFound)
	}
	anon.post(t, "/oauth/register", map[string]any{"redirect_uris": []string{mcpCallback}}).expect(t, http.StatusNotFound)
	anon.tokenCall(t, url.Values{"grant_type": {"refresh_token"}}).expect(t, http.StatusNotFound)
	owner.get(t, "/api/v1/authserver/get-authorization-request", authorizeQuery("00000000-0000-0000-0000-000000000001")).
		expect(t, http.StatusNotFound)
	owner.post(t, "/api/v1/authserver/approve-authorization", authorizeBody("00000000-0000-0000-0000-000000000001")).
		expect(t, http.StatusNotFound)
	owner.post(t, "/api/v1/authserver/decline-authorization", authorizeBody("00000000-0000-0000-0000-000000000001")).
		expect(t, http.StatusNotFound)
	owner.get(t, "/api/v1/user/get-connected-apps", nil).expect(t, http.StatusNotFound)
	owner.post(t, "/api/v1/user/revoke-connected-app", map[string]string{"grant_id": "00000000-0000-0000-0000-000000000001"}).
		expect(t, http.StatusNotFound)
	var st struct {
		OAuthServer bool `json:"oauth_server"`
	}
	anon.get(t, "/api/v1/auth/state", nil).expect(t, http.StatusOK).data(t, &st)
	if st.OAuthServer {
		t.Fatal("auth state reports a disabled OAuth server")
	}
}

// TestRevokeAllForUserRevokesGrants: the authn usecase's revoke-everything
// reaches the authorization server's grants (bootstrap wires the grant
// revoker), not only through the user row's cascade.
func TestRevokeAllForUserRevokesGrants(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	cid, tok := connectMCP(t, owner)
	if err := e.deps.Authn.RevokeAllForUser(e.ctx, owner.userID); err != nil {
		t.Fatal(err)
	}
	var oe struct {
		Error string `json:"error"`
	}
	oauthJSON(t, e.anon().tokenCall(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {cid},
		"refresh_token": {tok.RefreshToken}}).expect(t, http.StatusBadRequest), &oe)
	if oe.Error != "invalid_grant" {
		t.Fatalf("refresh after revoke-all: %q", oe.Error)
	}
}
