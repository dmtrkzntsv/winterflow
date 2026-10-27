// Package authn issues, authenticates and revokes opaque access tokens: login
// sessions (wfs_), personal access tokens (wfp_) and the OAuth authorization
// server's access tokens (wfo_), all in one store. A raw token is a prefix and
// 32 random bytes in base64url; only its SHA-256 is stored, so checking one is
// a single indexed lookup.
package authn

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/pat"
	"winterflow/pkg/util"
)

const (
	// tokenRandomBytes is the entropy of every token.
	tokenRandomBytes = 32

	// touchInterval throttles last_used_at writes: a busy client costs at
	// most one write a minute, not one per request.
	touchInterval = time.Minute
	// deadRetention is how long a revoked or expired token is kept (shown as
	// expired in lists) before Prune deletes it.
	deadRetention = 30 * 24 * time.Hour
)

// UseCase is the access-token store's business logic.
type UseCase struct {
	repo   port.AccessTokenRepository
	grants port.GrantRevoker
	now    func() time.Time
}

// New builds the usecase; now defaults to time.Now.
func New(repo port.AccessTokenRepository, now func() time.Time) *UseCase {
	if now == nil {
		now = time.Now
	}
	return &UseCase{repo: repo, now: now}
}

// SetGrantRevoker wires the authorization server's grants, so
// RevokeAllForUser revokes them too. Call it before serving; nil (the
// default) leaves grants alone.
func (u *UseCase) SetGrantRevoker(g port.GrantRevoker) { u.grants = g }

// prefixes mark each kind's raw tokens (PATs keep pkg/pat's).
var prefixes = map[model.TokenKind]string{
	model.TokenSession: "wfs_",
	model.TokenPAT:     pat.TokenPrefix,
	model.TokenOAuth:   "wfo_",
}

// issue stores token t (its ID, and a PAT's display prefix, filled in here)
// under a fresh raw token of its kind and returns both.
func (u *UseCase) issue(ctx context.Context, t model.AccessToken) (model.AccessToken, string, error) {
	b := make([]byte, tokenRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return model.AccessToken{}, "", err
	}
	raw := prefixes[t.Kind] + base64.RawURLEncoding.EncodeToString(b)
	t.ID = util.GenerateID()
	if t.Kind == model.TokenPAT {
		t.Prefix = raw[:pat.PrefixLen]
	}
	if err := u.repo.Insert(ctx, t, pat.Hash(raw)); err != nil {
		return model.AccessToken{}, "", err
	}
	return t, raw, nil
}

// Authenticate resolves a raw token to its principal: model.ErrUnauthorized
// for an unknown, expired or revoked one. It stamps last_used_at at most once
// a minute and slides a session's expiry (slideSession) in the same write.
func (u *UseCase) Authenticate(ctx context.Context, raw string) (model.Principal, error) {
	if raw == "" {
		return model.Principal{}, model.ErrUnauthorized
	}
	t, err := u.repo.GetByHash(ctx, pat.Hash(raw))
	if errors.Is(err, model.ErrNotFound) {
		return model.Principal{}, model.ErrUnauthorized
	}
	if err != nil {
		return model.Principal{}, err
	}
	now := u.now()
	if !t.IsLive(now) {
		return model.Principal{}, model.ErrUnauthorized
	}
	expiresAt, slid := t.ExpiresAt, false
	if t.Kind == model.TokenSession {
		expiresAt, slid = slideSession(t, now)
	}
	if slid || now.Sub(t.LastUsedAt) >= touchInterval {
		ok, err := u.repo.Touch(ctx, t.ID, now, expiresAt)
		if err != nil {
			return model.Principal{}, err
		}
		// The touch matches only an unrevoked row: a revoke landed between
		// the read and the write.
		if !ok {
			return model.Principal{}, model.ErrUnauthorized
		}
	}
	return model.Principal{UserID: t.UserID, TokenID: t.ID, Kind: t.Kind, Scope: t.Scope}, nil
}

// RevokeToken revokes one token, of any kind (a logout revokes the
// presenting session).
func (u *UseCase) RevokeToken(ctx context.Context, tokenID string) error {
	return u.repo.Revoke(ctx, tokenID, u.now())
}

// RevokeAllForUser revokes every session, PAT and OAuth access token of the
// user and, with a grant revoker wired, their OAuth grants: what removing a
// user from the org (or deleting them) must do.
func (u *UseCase) RevokeAllForUser(ctx context.Context, userID string) error {
	now := u.now()
	for _, kind := range []model.TokenKind{model.TokenSession, model.TokenPAT, model.TokenOAuth} {
		if err := u.repo.RevokeAllOfKind(ctx, userID, kind, now); err != nil {
			return err
		}
	}
	if u.grants != nil {
		return u.grants.RevokeUserGrants(ctx, userID)
	}
	return nil
}

// IssueOAuthAccess mints an OAuth access token (scope mcp) for the grant,
// valid for ttl; it never slides.
func (u *UseCase) IssueOAuthAccess(ctx context.Context, userID, grantID string, ttl time.Duration) (string, error) {
	now := u.now()
	exp := now.Add(ttl)
	_, raw, err := u.issue(ctx, model.AccessToken{
		UserID: userID, Kind: model.TokenOAuth, Scope: model.ScopeMCP, GrantID: grantID,
		CreatedAt: now, LastUsedAt: now, ExpiresAt: &exp,
	})
	return raw, err
}

// RevokeGrantTokens revokes every access token issued under an OAuth grant
// (the grant was revoked, replaced or caught replaying a refresh token).
func (u *UseCase) RevokeGrantTokens(ctx context.Context, grantID string) error {
	return u.repo.RevokeByGrant(ctx, grantID, u.now())
}

// Prune deletes tokens revoked or expired more than 30 days ago.
func (u *UseCase) Prune(ctx context.Context) error {
	_, err := u.repo.DeleteDead(ctx, u.now().Add(-deadRetention))
	return err
}

// MemberRemover is the slice of the user repository RemoveMember needs.
type MemberRemover interface {
	RemoveMember(ctx context.Context, orgID, callerID, userID string) error
}

// TokenRevoker is the slice of UseCase RemoveMember needs.
type TokenRevoker interface {
	RevokeAllForUser(ctx context.Context, userID string) error
}

// RemoveMember removes userID from orgID (deleting their account) and then
// revokes everything they could sign in with. Only a removal that went
// through revokes anything, so a caller cannot revoke a stranger's tokens.
func RemoveMember(ctx context.Context, users MemberRemover, tokens TokenRevoker, orgID, callerID, userID string) error {
	if err := users.RemoveMember(ctx, orgID, callerID, userID); err != nil {
		return err
	}
	return tokens.RevokeAllForUser(ctx, userID)
}
