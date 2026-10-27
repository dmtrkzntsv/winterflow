package oauthprovider

import (
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"winterflow/internal/domain/model"
)

type Issuer struct {
	ID              string
	IssuerURL       string
	ClientID        string
	ClientSecret    func(now time.Time) (string, error)
	Scopes          []string
	UsePKCE         bool
	ResponseMode    string
	TrustEmail      bool
	ExtraAuthParams map[string]string
	// IssuerAliases are extra literal iss values accepted for this issuer
	// (Google documents both "https://accounts.google.com" and "accounts.google.com").
	IssuerAliases []string
	// TokenAuthMethod pins how the code exchange authenticates the client
	// (AuthClientSecretBasic or AuthClientSecretPost); empty picks from the
	// discovery document's token_endpoint_auth_methods_supported.
	TokenAuthMethod string
}

// Client authentication methods at the token endpoint (OpenID Connect Core
// section 9).
const (
	AuthClientSecretBasic = "client_secret_basic"
	AuthClientSecretPost  = "client_secret_post"
)

// StaticSecret adapts a plain client secret to the ClientSecret seam.
func StaticSecret(secret string) func(time.Time) (string, error) {
	return func(time.Time) (string, error) { return secret, nil }
}

type Discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	// TokenEndpointAuthMethods is token_endpoint_auth_methods_supported;
	// absent means client_secret_basic only (OpenID Connect Discovery).
	TokenEndpointAuthMethods []string `json:"token_endpoint_auth_methods_supported"`
}

// Claims and Tokens are the domain's shapes: the sign-in usecase consumes
// this client through its own interface.
type (
	Claims = model.ProviderClaims
	Tokens = model.ProviderTokens
)

type Client struct {
	issuer Issuer
	http   *http.Client

	// now is the clock the JWKS cache runs on (tests inject one).
	now func() time.Time

	mu         sync.Mutex
	disc       *Discovery
	keys       map[string]crypto.PublicKey
	keysUntil  time.Time // the cached JWKS is refetched from then on
	lastMissAt time.Time
}

func NewClient(issuer Issuer, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	if hc.CheckRedirect == nil {
		// A copy, so the caller's client keeps its own policy.
		cp := *hc
		cp.CheckRedirect = secureRedirect
		hc = &cp
	}
	issuer.IssuerURL = strings.TrimSuffix(issuer.IssuerURL, "/")
	return &Client{issuer: issuer, http: hc, now: time.Now}
}

// ValidateIssuerURL enforces on a configured issuer the rule validateEndpoint
// applies to discovered endpoints: an absolute https URL (plain http only on
// a loopback host), without userinfo, query or fragment. The discovery
// document fetched from it names the JWKS and token endpoints, so a
// plaintext fetch would let anyone on the path substitute their own.
func ValidateIssuerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" || u.Opaque != "" {
		return fmt.Errorf("oidc: issuer %q is not an absolute URL", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return fmt.Errorf("oidc: issuer %q must not carry userinfo, a query or a fragment", raw)
	}
	if u.Scheme == "https" || (u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return nil
	}
	return fmt.Errorf("oidc: issuer %q must use https (http only on a loopback host)", raw)
}

// secureRedirect follows at most 10 redirects and none that downgrades to
// plain http off loopback, so an https issuer cannot be bounced to a
// plaintext discovery, JWKS or token request.
func secureRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("oidc: stopped after 10 redirects")
	}
	if req.URL.Scheme == "https" || (req.URL.Scheme == "http" && isLoopbackHost(req.URL.Hostname())) {
		return nil
	}
	return fmt.Errorf("oidc: refusing a redirect to %s", req.URL.Redacted())
}

func (c *Client) Issuer() Issuer { return c.issuer }

// UsesPKCE reports whether the authorization request carries a PKCE
// challenge (Apple's does not: its signed client secret and the nonce bind
// the exchange).
func (c *Client) UsesPKCE() bool { return c.issuer.UsePKCE }

// TrustsEmail reports whether the provider's email claim counts as verified
// without an email_verified claim saying so.
func (c *Client) TrustsEmail() bool { return c.issuer.TrustEmail }

// IssuerURL is the configured issuer, without a trailing slash.
func (c *Client) IssuerURL() string { return c.issuer.IssuerURL }

// Discover loads the OpenID configuration once per process (lazily), so an
// issuer that is down at boot only fails the requests that need it.
func (c *Client) Discover(ctx context.Context) (Discovery, error) {
	c.mu.Lock()
	if c.disc != nil {
		d := *c.disc
		c.mu.Unlock()
		return d, nil
	}
	c.mu.Unlock()
	if err := ValidateIssuerURL(c.issuer.IssuerURL); err != nil {
		return Discovery{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.issuer.IssuerURL+"/.well-known/openid-configuration", nil)
	if err != nil {
		return Discovery{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Discovery{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Discovery{}, fmt.Errorf("oidc: discovery returned status %d", resp.StatusCode)
	}
	var d Discovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d); err != nil {
		return Discovery{}, err
	}
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return Discovery{}, errors.New("oidc: discovery document lacks required endpoints")
	}
	for _, ep := range []struct {
		name     string
		value    string
		required bool
	}{
		{"authorization_endpoint", d.AuthorizationEndpoint, true},
		{"token_endpoint", d.TokenEndpoint, true},
		{"jwks_uri", d.JWKSURI, true},
		{"userinfo_endpoint", d.UserinfoEndpoint, false},
	} {
		if ep.value == "" && !ep.required {
			continue
		}
		if err := validateEndpoint(ep.name, ep.value); err != nil {
			return Discovery{}, err
		}
	}
	// A document served from the configured URL but claiming another issuer is
	// either a misconfiguration or a redirect to a foreign IdP; its endpoints
	// must not be trusted, and its ID tokens would pass the issuer check below.
	if iss := strings.TrimSuffix(d.Issuer, "/"); iss != c.issuer.IssuerURL {
		return Discovery{}, fmt.Errorf("oidc: discovery issuer %q does not match the configured issuer %q", d.Issuer, c.issuer.IssuerURL)
	}
	c.mu.Lock()
	c.disc = &d
	c.mu.Unlock()
	return d, nil
}

// validateEndpoint enforces the OpenID Connect Discovery rule that provider
// endpoints are absolute HTTPS URLs: an http token/JWKS/userinfo endpoint
// would hand the client secret, the signing keys or the access token to
// anyone on the path, and a non-http(s) authorization endpoint would be
// handed to the browser verbatim. Plain http is allowed for loopback hosts
// only (local development issuers).
func validateEndpoint(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("oidc: discovery %s is not an absolute URL", name)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("oidc: discovery %s must use https", name)
}

// isLoopbackHost reports a host name or IP that only reaches this machine.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *Client) AuthURL(ctx context.Context, state, nonce, codeChallenge, redirectURI string) (string, error) {
	d, err := c.Discover(ctx)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", c.issuer.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", strings.Join(c.issuer.Scopes, " "))
	q.Set("state", state)
	q.Set("nonce", nonce)
	if c.issuer.UsePKCE {
		q.Set("code_challenge", codeChallenge)
		q.Set("code_challenge_method", "S256")
	}
	if c.issuer.ResponseMode != "" {
		q.Set("response_mode", c.issuer.ResponseMode)
	}
	for k, v := range c.issuer.ExtraAuthParams {
		q.Set(k, v)
	}
	sep := "?"
	if strings.Contains(d.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return d.AuthorizationEndpoint + sep + q.Encode(), nil
}

// tokenAuthMethod is the issuer's pinned method, else client_secret_basic
// when the discovery document lists it or lists nothing (the specification's
// default), else client_secret_post when listed. An issuer offering neither
// cannot take a client secret.
func (c *Client) tokenAuthMethod(d Discovery) (string, error) {
	if c.issuer.TokenAuthMethod != "" {
		return c.issuer.TokenAuthMethod, nil
	}
	switch {
	case len(d.TokenEndpointAuthMethods) == 0, slices.Contains(d.TokenEndpointAuthMethods, AuthClientSecretBasic):
		return AuthClientSecretBasic, nil
	case slices.Contains(d.TokenEndpointAuthMethods, AuthClientSecretPost):
		return AuthClientSecretPost, nil
	}
	return "", fmt.Errorf("oidc: token endpoint supports neither %s nor %s (it lists %q)",
		AuthClientSecretBasic, AuthClientSecretPost, d.TokenEndpointAuthMethods)
}

// Exchange redeems the code, authenticating with client_secret_basic or
// client_secret_post (tokenAuthMethod).
func (c *Client) Exchange(ctx context.Context, code, codeVerifier, redirectURI string, now time.Time) (Tokens, error) {
	d, err := c.Discover(ctx)
	if err != nil {
		return Tokens{}, err
	}
	method, err := c.tokenAuthMethod(d)
	if err != nil {
		return Tokens{}, err
	}
	secret, err := c.issuer.ClientSecret(now)
	if err != nil {
		return Tokens{}, err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	if method == AuthClientSecretPost {
		form.Set("client_id", c.issuer.ClientID)
		form.Set("client_secret", secret)
	}
	if c.issuer.UsePKCE {
		form.Set("code_verifier", codeVerifier)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	if method == AuthClientSecretBasic {
		// RFC 6749 section 2.3.1: both halves form-urlencoded first.
		req.SetBasicAuth(url.QueryEscape(c.issuer.ClientID), url.QueryEscape(secret))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Tokens{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return Tokens{}, fmt.Errorf("oidc: token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || body.Error != "" {
		return Tokens{}, fmt.Errorf("oidc: token endpoint status %d error %q", resp.StatusCode, body.Error)
	}
	if body.IDToken == "" {
		return Tokens{}, errors.New("oidc: token response carries no id_token")
	}
	return Tokens{IDToken: body.IDToken, AccessToken: body.AccessToken}, nil
}

func (c *Client) VerifyIDToken(ctx context.Context, raw, nonce string, now time.Time) (Claims, error) {
	h, cl, input, sig, err := parseCompact(raw)
	if err != nil {
		return Claims{}, err
	}
	if h.Alg != "RS256" && h.Alg != "ES256" {
		return Claims{}, fmt.Errorf("%w: unsupported alg %q", ErrInvalidToken, h.Alg)
	}
	key, err := c.keyFor(ctx, h.Kid)
	if err != nil {
		return Claims{}, err
	}
	if err := verifySignature(h.Alg, key, input, sig); err != nil {
		return Claims{}, err
	}
	d, err := c.Discover(ctx)
	if err != nil {
		return Claims{}, err
	}
	iss := strings.TrimSuffix(cl.Iss, "/")
	if iss != strings.TrimSuffix(d.Issuer, "/") && iss != c.issuer.IssuerURL && !slices.Contains(c.issuer.IssuerAliases, iss) {
		return Claims{}, fmt.Errorf("%w: issuer %q", ErrInvalidToken, cl.Iss)
	}
	if err := audienceMatches(cl.Aud, cl.Azp, c.issuer.ClientID); err != nil {
		return Claims{}, err
	}
	if cl.Exp == 0 || now.After(time.Unix(cl.Exp, 0).Add(clockSkew)) {
		return Claims{}, fmt.Errorf("%w: expired", ErrInvalidToken)
	}
	if cl.Iat != 0 && time.Unix(cl.Iat, 0).After(now.Add(clockSkew)) {
		return Claims{}, fmt.Errorf("%w: issued in the future", ErrInvalidToken)
	}
	if nonce != "" && cl.Nonce != nonce {
		return Claims{}, fmt.Errorf("%w: nonce", ErrInvalidToken)
	}
	if cl.Sub == "" {
		return Claims{}, fmt.Errorf("%w: missing sub", ErrInvalidToken)
	}
	return cl.toClaims(), nil
}

// UserInfo fetches the userinfo document; the caller decides how to merge it.
func (c *Client) UserInfo(ctx context.Context, accessToken string) (Claims, error) {
	d, err := c.Discover(ctx)
	if err != nil {
		return Claims{}, err
	}
	if d.UserinfoEndpoint == "" {
		return Claims{}, errors.New("oidc: issuer publishes no userinfo endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.UserinfoEndpoint, nil)
	if err != nil {
		return Claims{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Claims{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Claims{}, fmt.Errorf("oidc: userinfo returned status %d", resp.StatusCode)
	}
	var cl idTokenClaims
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&cl); err != nil {
		return Claims{}, err
	}
	return cl.toClaims(), nil
}
