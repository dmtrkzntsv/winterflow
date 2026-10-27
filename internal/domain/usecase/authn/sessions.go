package authn

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
)

const (
	// SessionTTL is a session's sliding window: it ends 30 days after its
	// last use...
	SessionTTL = 30 * 24 * time.Hour
	// SessionMaxAge ...and 90 days after sign-in at the latest.
	SessionMaxAge = 90 * 24 * time.Hour
	// sessionSlideStep is how far a session's expiry must have fallen behind
	// before a use moves it, so the expiry is rewritten about once a day.
	sessionSlideStep = 24 * time.Hour
)

// IssueSession signs the user in: a new session token for the browser.
func (u *UseCase) IssueSession(ctx context.Context, userID, userAgent string) (string, error) {
	now := u.now()
	exp := now.Add(SessionTTL)
	_, raw, err := u.issue(ctx, model.AccessToken{
		UserID: userID, Kind: model.TokenSession, Scope: model.ScopeFull, UserAgent: userAgent,
		CreatedAt: now, LastUsedAt: now, ExpiresAt: &exp,
	})
	return raw, err
}

// RevokeSessions signs the user out everywhere; PATs and OAuth grants are
// left alone.
func (u *UseCase) RevokeSessions(ctx context.Context, userID string) error {
	return u.repo.RevokeAllOfKind(ctx, userID, model.TokenSession, u.now())
}

// slideSession returns the expiry a use at now gives session t: SessionTTL
// past now, capped at SessionMaxAge past its creation. slid is false (and
// the stored expiry kept) unless that moves it by more than
// sessionSlideStep.
func slideSession(t model.AccessToken, now time.Time) (*time.Time, bool) {
	exp := now.Add(SessionTTL)
	if limit := t.CreatedAt.Add(SessionMaxAge); exp.After(limit) {
		exp = limit
	}
	if t.ExpiresAt != nil && exp.Sub(*t.ExpiresAt) <= sessionSlideStep {
		return t.ExpiresAt, false
	}
	return &exp, true
}
