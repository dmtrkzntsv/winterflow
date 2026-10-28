package model

import (
	"errors"
	"time"
)

// ErrUnauthorized is returned for a credential that does not authenticate:
// an unknown, expired or revoked access token alike, so the answer is no
// oracle.
var ErrUnauthorized = errors.New("unauthorized")

// TokenKind is what an access token is for (access_tokens.kind).
type TokenKind string

const (
	// TokenSession is a browser login session (wfs_…).
	TokenSession TokenKind = "session"
	// TokenPAT is a personal access token (wfp_…).
	TokenPAT TokenKind = "pat"
	// TokenOAuth is an access token the OAuth authorization server issued
	// to an MCP client (wfo_…), linked to its grant.
	TokenOAuth TokenKind = "oauth"
)

// Token scopes (access_tokens.scope): sessions and PATs reach everything
// the user may; OAuth tokens only /mcp.
const (
	ScopeFull = "full"
	ScopeMCP  = "mcp"
)

// Principal is who an authenticated request acts as, and through which
// token.
type Principal struct {
	UserID  string
	TokenID string
	Kind    TokenKind
	Scope   string
}

// AccessToken is a stored access token. The raw token is never stored, only
// its SHA-256; Prefix (PATs only) identifies a token in lists. LastUsedAt is
// zero for a token never used.
type AccessToken struct {
	ID         string
	UserID     string
	Kind       TokenKind
	Scope      string
	Name       string
	Prefix     string
	UserAgent  string
	GrantID    string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

// IsLive reports whether the token still authenticates at now.
func (t AccessToken) IsLive(now time.Time) bool {
	if t.RevokedAt != nil {
		return false
	}
	return t.ExpiresAt == nil || t.ExpiresAt.After(now)
}
