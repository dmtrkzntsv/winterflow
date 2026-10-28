package model

import "time"

// The OAuth 2.1 authorization server (Winterflow as the server MCP clients
// sign in to). Clients register themselves (RFC 7591), a user approves one
// on the consent page, which yields a one-shot code; the code becomes a
// grant holding a rotating refresh token, and every access token (an
// access_tokens row of kind oauth) names its grant.

// OAuthClient is a dynamically registered client. SecretHash is set only
// for a confidential client; LastUsedAt stays nil until a user approves it.
type OAuthClient struct {
	ID           string
	Name         string
	RedirectURIs []string
	SecretHash   *string
	CreatedAt    time.Time
	LastUsedAt   *time.Time
}

// OAuthCode is an issued authorization code, stored by its SHA-256.
type OAuthCode struct {
	CodeHash      string
	ClientID      string
	UserID        string
	RedirectURI   string
	CodeChallenge string
	Resource      string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

// OAuthGrant is one user approval of one client; it holds the rotating
// refresh token. PrevRefreshTokenHash and RotatedAt remember the token
// rotated away from, so a duplicate refresh inside the grace window is told
// apart from a replay.
type OAuthGrant struct {
	ID                   string
	UserID               string
	ClientID             string
	RefreshTokenHash     string
	PrevRefreshTokenHash *string
	RotatedAt            *time.Time
	CreatedAt            time.Time
	LastUsedAt           time.Time
	ExpiresAt            time.Time
	RevokedAt            *time.Time
}

// IsLive reports whether the grant can still be refreshed at now.
func (g OAuthGrant) IsLive(now time.Time) bool { return g.RevokedAt == nil && g.ExpiresAt.After(now) }

// ConnectedGrant is a grant joined with its client, for the connected apps
// list.
type ConnectedGrant struct {
	Grant        OAuthGrant
	ClientName   string
	RedirectURIs []string
}

// ClientRegistration is a dynamic client registration request (RFC 7591).
type ClientRegistration struct {
	ClientName              string
	RedirectURIs            []string
	GrantTypes              []string
	ResponseTypes           []string
	TokenEndpointAuthMethod string
}

// RegisteredClient is the registration's answer. ClientSecret (and
// ClientSecretExpiresAt, 0: never) only for a confidential client.
type RegisteredClient struct {
	ClientID                string
	ClientIDIssuedAt        int64
	ClientName              string
	RedirectURIs            []string
	GrantTypes              []string
	ResponseTypes           []string
	TokenEndpointAuthMethod string
	ClientSecret            string
	ClientSecretExpiresAt   *int64
}

// AuthorizationRequest is the OAuth parameters of /oauth/authorize, as the
// consent page forwards them.
type AuthorizationRequest struct {
	ClientID            string
	RedirectURI         string
	ResponseType        string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
	Scope               string
	State               string
}

// AuthorizationDescription is what the consent page shows. A non-empty
// ErrorRedirectURL means the request is bad but its redirect is trusted:
// the page navigates there instead of asking.
type AuthorizationDescription struct {
	ClientName       string
	RedirectHost     string
	IsLoopback       bool
	ErrorRedirectURL string
}

// TokenRequest is a /oauth/token request's parameters.
type TokenRequest struct {
	GrantType, Code, RedirectURI, CodeVerifier, RefreshToken, Resource, Scope, ClientID, ClientSecret string
}

// TokenResponse is the token endpoint's success body (RFC 6749 5.1).
type TokenResponse struct {
	AccessToken  string `json:"access_token" required:"true"`
	TokenType    string `json:"token_type" required:"true"`
	ExpiresIn    int    `json:"expires_in" required:"true"`
	RefreshToken string `json:"refresh_token" required:"true"`
	Scope        string `json:"scope" required:"true"`
}

// ConnectedApp is one live grant as the connected apps page lists it.
type ConnectedApp struct {
	GrantID      string
	ClientName   string
	RedirectHost string
	IsLoopback   bool
	CreatedAt    time.Time
	LastUsedAt   time.Time
}

// Errors of the consent and connected-app routes. A client or redirect that
// cannot be trusted is shown to the user (400), never redirected to.
var (
	ErrAuthServerDisabled = &classified{"connecting apps is not available on this server", ErrNotFound}
	ErrOAuthClientUnknown = &classified{"this app's registration has expired or is unknown: remove Winterflow from the app and add it again", ErrRejected}
	ErrOAuthRedirect      = &classified{"this sign-in link is not valid for this app: start the connection again from the app", ErrRejected}
	ErrGrantNotFound      = &classified{"connected app not found", ErrNotFound}
)
