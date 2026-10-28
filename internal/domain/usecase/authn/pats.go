package authn

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"

	"winterflow/internal/domain/model"
)

// CreatePAT mints a personal access token (no expiry when expiresAt is
// nil). The raw token is returned once and never stored.
func (u *UseCase) CreatePAT(ctx context.Context, userID, name string, expiresAt *time.Time) (model.AccessToken, string, error) {
	return u.issue(ctx, model.AccessToken{
		UserID: userID, Kind: model.TokenPAT, Scope: model.ScopeFull, Name: name,
		CreatedAt: u.now(), ExpiresAt: expiresAt,
	})
}

// ListPATs returns the user's PATs not revoked, newest first; expired ones
// stay listed (as expired) until Prune deletes them.
func (u *UseCase) ListPATs(ctx context.Context, userID string) ([]model.AccessToken, error) {
	rows, err := u.repo.ListByUser(ctx, userID, model.TokenPAT)
	if err != nil {
		return nil, err
	}
	out := make([]model.AccessToken, 0, len(rows))
	for _, t := range rows {
		if t.RevokedAt == nil {
			out = append(out, t)
		}
	}
	slices.Reverse(out)
	return out, nil
}

// DeletePAT revokes one of the user's PATs: model.ErrTokenNotFound for an
// unknown id, another user's token or one that is not a PAT.
func (u *UseCase) DeletePAT(ctx context.Context, userID, tokenID string) error {
	// Token ids are UUIDs; on Postgres anything else could not even be
	// looked up.
	if id, err := uuid.Parse(tokenID); err != nil || id.String() != tokenID {
		return model.ErrTokenNotFound
	}
	ok, err := u.repo.RevokeUserToken(ctx, userID, model.TokenPAT, tokenID, u.now())
	if err != nil {
		return err
	}
	if !ok {
		return model.ErrTokenNotFound
	}
	return nil
}
