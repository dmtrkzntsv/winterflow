package authserver

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"winterflow/internal/domain/model"
	"winterflow/pkg/util"
)

var (
	errInvalidGrant = &OAuthError{400, "invalid_grant", "the grant is invalid, expired or revoked"}
	errBadClient    = &OAuthError{401, "invalid_client", "client authentication failed"}
	errBadTarget    = &OAuthError{400, "invalid_target", "unknown resource"}
)

func missingParam(name string) *OAuthError {
	return &OAuthError{400, "invalid_request", name + " is required"}
}

// Token is the token endpoint: authorization_code and refresh_token
// grants. Every error is an *OAuthError, except a store failure.
func (u *UseCase) Token(ctx context.Context, req model.TokenRequest) (model.TokenResponse, error) {
	if !u.Enabled() {
		return model.TokenResponse{}, errDisabled
	}
	switch req.GrantType {
	case "authorization_code":
		return u.exchange(ctx, req)
	case "refresh_token":
		return u.refresh(ctx, req)
	}
	return model.TokenResponse{}, &OAuthError{400, "unsupported_grant_type", "unsupported grant_type"}
}

// authenticateClient finds the client; a confidential one must present its
// secret (hashes compared in constant time), a public one has none.
func (u *UseCase) authenticateClient(ctx context.Context, clientID, secret string) (model.OAuthClient, error) {
	if id, err := uuid.Parse(clientID); err != nil || id.String() != clientID {
		return model.OAuthClient{}, errBadClient
	}
	c, err := u.repo.GetClient(ctx, clientID)
	if errors.Is(err, model.ErrNotFound) {
		return model.OAuthClient{}, errBadClient
	}
	if err != nil {
		return model.OAuthClient{}, err
	}
	if c.SecretHash != nil && subtle.ConstantTimeCompare([]byte(hashSecret(secret)), []byte(*c.SecretHash)) != 1 {
		return model.OAuthClient{}, errBadClient
	}
	return c, nil
}

func (u *UseCase) exchange(ctx context.Context, req model.TokenRequest) (model.TokenResponse, error) {
	if req.ClientID == "" {
		return model.TokenResponse{}, errBadClient
	}
	switch {
	case req.Code == "":
		return model.TokenResponse{}, missingParam("code")
	case req.RedirectURI == "":
		return model.TokenResponse{}, missingParam("redirect_uri")
	case req.CodeVerifier == "":
		return model.TokenResponse{}, missingParam("code_verifier")
	}
	c, err := u.authenticateClient(ctx, req.ClientID, req.ClientSecret)
	if err != nil {
		return model.TokenResponse{}, err
	}
	now := u.now()
	// Consumed in its own statement before any check, so every attempt,
	// a failed one included, burns the code.
	code, err := u.repo.ConsumeCode(ctx, hashSecret(req.Code))
	if errors.Is(err, model.ErrNotFound) {
		return model.TokenResponse{}, errInvalidGrant
	}
	if err != nil {
		return model.TokenResponse{}, err
	}
	if !now.Before(code.ExpiresAt) || code.ClientID != c.ID || code.RedirectURI != req.RedirectURI || !VerifyPKCE(req.CodeVerifier, code.CodeChallenge) {
		return model.TokenResponse{}, errInvalidGrant
	}
	if !u.resourceOK(req.Resource) {
		return model.TokenResponse{}, errBadTarget
	}
	var resp model.TokenResponse
	err = u.tx.WithTx(ctx, func(ctx context.Context) error {
		rawRefresh, refreshHash, err := newSecret(refreshPrefix)
		if err != nil {
			return err
		}
		g := model.OAuthGrant{ID: util.GenerateID(), UserID: code.UserID, ClientID: c.ID, RefreshTokenHash: refreshHash,
			CreatedAt: now, LastUsedAt: now, ExpiresAt: now.Add(GrantIdleTTL)}
		if err := u.repo.InsertGrant(ctx, g); err != nil {
			return err
		}
		access, err := u.tokens.IssueOAuthAccess(ctx, code.UserID, g.ID, AccessTokenTTL)
		if err != nil {
			return err
		}
		// Re-authorizing a client replaces the user's earlier connection
		// to it.
		replaced, err := u.repo.RevokeOtherGrants(ctx, code.UserID, c.ID, g.ID, now)
		if err != nil {
			return err
		}
		for _, id := range replaced {
			if err := u.tokens.RevokeGrantTokens(ctx, id); err != nil {
				return err
			}
		}
		resp = tokenResponse(access, rawRefresh)
		return nil
	})
	if err != nil {
		return model.TokenResponse{}, err
	}
	u.housekeeping(ctx, now)
	return resp, nil
}

func tokenResponse(access, refresh string) model.TokenResponse {
	return model.TokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: int(AccessTokenTTL / time.Second),
		RefreshToken: refresh, Scope: Scope}
}

// housekeepingDue elects at most one caller per HousekeepingInterval; the
// first call after start always wins, and a clock stepped backwards counts
// as due so the throttle cannot stall until the old time comes round again.
func (u *UseCase) housekeepingDue(now time.Time) bool {
	u.hkMu.Lock()
	defer u.hkMu.Unlock()
	if !u.hkLast.IsZero() && !now.Before(u.hkLast) && now.Sub(u.hkLast) < HousekeepingInterval {
		return false
	}
	u.hkLast = now
	return true
}

// housekeeping purges expired codes and long-dead grants (with their spent
// refresh hashes). Dead access tokens are the authn usecase's hourly prune.
func (u *UseCase) housekeeping(ctx context.Context, now time.Time) {
	if !u.housekeepingDue(now) {
		return
	}
	if err := u.repo.PurgeExpiredCodes(ctx, now); err != nil {
		slog.WarnContext(ctx, "authserver: code purge failed", "error", err)
	}
	if _, err := u.repo.DeleteDeadGrants(ctx, now.Add(-DeadRetention)); err != nil {
		slog.WarnContext(ctx, "authserver: grant purge failed", "error", err)
	}
}

func (u *UseCase) refresh(ctx context.Context, req model.TokenRequest) (model.TokenResponse, error) {
	if req.ClientID == "" {
		return model.TokenResponse{}, errBadClient
	}
	if req.RefreshToken == "" {
		return model.TokenResponse{}, missingParam("refresh_token")
	}
	c, err := u.authenticateClient(ctx, req.ClientID, req.ClientSecret)
	if err != nil {
		return model.TokenResponse{}, err
	}
	if !u.resourceOK(req.Resource) {
		return model.TokenResponse{}, errBadTarget
	}
	now := u.now()
	hash := hashSecret(req.RefreshToken)
	g, err := u.repo.GetGrantByRefreshHash(ctx, hash)
	if errors.Is(err, model.ErrNotFound) {
		return model.TokenResponse{}, u.handleRotatedRefresh(ctx, hash, c.ID, now)
	}
	if err != nil {
		return model.TokenResponse{}, err
	}
	if g.ClientID != c.ID || !g.IsLive(now) {
		return model.TokenResponse{}, errInvalidGrant
	}
	var resp model.TokenResponse
	err = u.tx.WithTx(ctx, func(ctx context.Context) error {
		rawRefresh, newHash, err := newSecret(refreshPrefix)
		if err != nil {
			return err
		}
		// Conditional on the presented hash still being current and the
		// grant unrevoked: a concurrent refresh, or a revoke that landed
		// first, makes this match nothing.
		n, err := u.repo.RotateGrant(ctx, g.ID, hash, newHash, now, now.Add(GrantIdleTTL))
		if err != nil {
			return err
		}
		if n == 0 {
			return errInvalidGrant
		}
		if err := u.repo.InsertSpentRefreshHash(ctx, g.ID, hash, now); err != nil {
			return err
		}
		access, err := u.tokens.IssueOAuthAccess(ctx, g.UserID, g.ID, AccessTokenTTL)
		if err != nil {
			return err
		}
		resp = tokenResponse(access, rawRefresh)
		return nil
	})
	if err != nil {
		return model.TokenResponse{}, err
	}
	u.housekeeping(ctx, now)
	return resp, nil
}

// handleRotatedRefresh answers a refresh token already rotated away, found
// in the spent table however many rotations ago. Only the token rotated
// away MOST RECENTLY, and only inside the grace window, is a concurrent
// refresh (two processes sharing stored credentials) or a retry whose
// answer was lost, so it is only refused. Any other spent token, at any
// age, is circulating where it should not (a thief can rotate more than
// once before the legitimate client retries), so the whole grant goes.
func (u *UseCase) handleRotatedRefresh(ctx context.Context, hash, clientID string, now time.Time) error {
	g, err := u.repo.GetGrantBySpentRefreshHash(ctx, hash)
	if errors.Is(err, model.ErrNotFound) {
		return errInvalidGrant
	}
	if err != nil {
		return err
	}
	if g.ClientID != clientID || g.RevokedAt != nil {
		return errInvalidGrant
	}
	latest := g.PrevRefreshTokenHash != nil && *g.PrevRefreshTokenHash == hash
	// Absolute: instance clocks may differ slightly, but a large backward
	// step must not stretch the window and suppress revocation.
	var elapsed time.Duration
	if g.RotatedAt != nil {
		elapsed = now.Sub(*g.RotatedAt)
		if elapsed < 0 {
			elapsed = -elapsed
		}
	}
	if latest && g.RotatedAt != nil && elapsed <= RefreshGrace {
		slog.WarnContext(ctx, "authserver: refresh token reused", "grant_id", g.ID, "revoked", false)
		return errInvalidGrant
	}
	if err := u.revokeGrant(ctx, g.ID, now); err != nil {
		return err
	}
	slog.WarnContext(ctx, "authserver: refresh token reused", "grant_id", g.ID, "revoked", true)
	return errInvalidGrant
}
