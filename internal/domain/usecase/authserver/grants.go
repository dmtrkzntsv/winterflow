package authserver

import (
	"context"
	"errors"
	"time"

	"winterflow/internal/domain/model"
)

// revokeGrant revokes the grant and every access token issued under it, in
// one transaction.
func (u *UseCase) revokeGrant(ctx context.Context, id string, now time.Time) error {
	return u.tx.WithTx(ctx, func(ctx context.Context) error {
		if _, err := u.repo.RevokeGrant(ctx, id, now); err != nil {
			return err
		}
		return u.tokens.RevokeGrantTokens(ctx, id)
	})
}

// ListConnectedApps lists the user's live grants, oldest first.
func (u *UseCase) ListConnectedApps(ctx context.Context, userID string) ([]model.ConnectedApp, error) {
	rows, err := u.repo.ListUnrevokedGrants(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := u.now()
	out := []model.ConnectedApp{}
	for _, r := range rows {
		if !r.Grant.IsLive(now) {
			continue
		}
		host, loopback := "", false
		if len(r.RedirectURIs) > 0 {
			host, loopback = RedirectHost(r.RedirectURIs[0])
		}
		out = append(out, model.ConnectedApp{
			GrantID: r.Grant.ID, ClientName: r.ClientName, RedirectHost: host, IsLoopback: loopback,
			CreatedAt: r.Grant.CreatedAt, LastUsedAt: r.Grant.LastUsedAt,
		})
	}
	return out, nil
}

// RevokeConnectedApp revokes one of the user's grants with its access
// tokens; model.ErrGrantNotFound for an unknown, foreign or already revoked
// one.
func (u *UseCase) RevokeConnectedApp(ctx context.Context, userID, grantID string) error {
	g, err := u.repo.GetGrant(ctx, grantID)
	if errors.Is(err, model.ErrNotFound) {
		return model.ErrGrantNotFound
	}
	if err != nil {
		return err
	}
	if g.UserID != userID || g.RevokedAt != nil {
		return model.ErrGrantNotFound
	}
	return u.revokeGrant(ctx, grantID, u.now())
}

// RevokeUserGrants revokes every grant of the user (port.GrantRevoker: the
// authn usecase calls it when a user is removed, after revoking their
// access tokens of every kind).
func (u *UseCase) RevokeUserGrants(ctx context.Context, userID string) error {
	_, err := u.repo.RevokeUserGrants(ctx, userID, u.now())
	return err
}
