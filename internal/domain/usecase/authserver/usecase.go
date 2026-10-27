// Package authserver is the OAuth 2.1 authorization server MCP clients use
// to obtain tokens for /mcp: discovery, dynamic client registration
// (RFC 7591), authorization code with PKCE S256 only, rotating refresh
// tokens with theft detection, and the user's connected apps. It is a port
// of econumo's internal/authserver.
//
// Access tokens are the authn usecase's (kind oauth, scope mcp, linked to
// their grant); this package keeps clients, codes and grants.
package authserver

import (
	"context"
	"sync"
	"time"

	"winterflow/internal/domain/port"
)

const (
	// Scope is the one scope every grant carries.
	Scope = "mcp"
	// CodeTTL bounds an authorization code's life.
	CodeTTL = 2 * time.Minute
	// AccessTokenTTL is an access token's life; it never slides.
	AccessTokenTTL = time.Hour
	// GrantIdleTTL ends a grant this long after its last refresh.
	GrantIdleTTL = 90 * 24 * time.Hour
	// RefreshGrace is the window in which a replay of the latest rotated
	// refresh token is a concurrent refresh or a lost answer, not theft.
	RefreshGrace = 60 * time.Second
	// UnusedClientTTL is how long a registered client nobody approved is
	// kept.
	UnusedClientTTL = 30 * 24 * time.Hour
	// DeadRetention keeps revoked or expired grants this long before
	// housekeeping deletes them.
	DeadRetention = 30 * 24 * time.Hour
	// HousekeepingInterval throttles the purge a token request runs.
	HousekeepingInterval = 10 * time.Minute

	// MetadataPath is the protected resource metadata of /mcp (RFC 9728).
	MetadataPath = "/.well-known/oauth-protected-resource/mcp"
)

// Tokens is the slice of the authn usecase the server needs.
type Tokens interface {
	IssueOAuthAccess(ctx context.Context, userID, grantID string, ttl time.Duration) (string, error)
	RevokeGrantTokens(ctx context.Context, grantID string) error
}

// TxRunner runs fn in one transaction (backend.TxManager).
type TxRunner interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// UseCase is the authorization server.
type UseCase struct {
	repo   port.AuthServerRepository
	tokens Tokens
	tx     TxRunner
	now    func() time.Time
	issuer string

	hkMu   sync.Mutex
	hkLast time.Time
}

// New builds the server for issuer, the instance's public origin
// (config.GetPublicOrigin: an https origin with no path, or http on
// loopback); an empty issuer leaves it disabled. now defaults to time.Now.
func New(repo port.AuthServerRepository, tokens Tokens, tx TxRunner, issuer string, now func() time.Time) *UseCase {
	if now == nil {
		now = time.Now
	}
	return &UseCase{repo: repo, tokens: tokens, tx: tx, now: now, issuer: issuer}
}

// Enabled reports whether the server runs (WEB_URL is a usable origin).
func (u *UseCase) Enabled() bool { return u != nil && u.issuer != "" }

// Issuer is the server's issuer identifier (the public origin).
func (u *UseCase) Issuer() string { return u.issuer }

// ResourceURL is the one protected resource, /mcp.
func (u *UseCase) ResourceURL() string { return u.issuer + "/mcp" }

// ResourceMetadataURL is where /mcp's protected resource metadata lives,
// for the WWW-Authenticate challenge of an unauthenticated /mcp call; ""
// when disabled.
func (u *UseCase) ResourceMetadataURL() string {
	if !u.Enabled() {
		return ""
	}
	return u.issuer + MetadataPath
}

// Challenge is the WWW-Authenticate value of a 401 from /mcp; "" when
// disabled.
func (u *UseCase) Challenge() string {
	if !u.Enabled() {
		return ""
	}
	return `Bearer resource_metadata="` + u.ResourceMetadataURL() + `", scope="` + Scope + `"`
}
