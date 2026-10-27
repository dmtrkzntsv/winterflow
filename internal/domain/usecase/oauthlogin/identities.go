package oauthlogin

import (
	"context"

	"winterflow/internal/domain/model"
)

// Identities lists the user's linked provider accounts, oldest first.
func (s *Service) Identities(ctx context.Context, userID string) ([]model.Identity, error) {
	return s.repo.Identities(ctx, userID)
}

// Unlink removes the user's account of provider: model.ErrIdentityNotFound
// when there is none, model.ErrLastSignInMethod when the user has no
// password and no other linked provider.
func (s *Service) Unlink(ctx context.Context, userID, provider string) error {
	if !knownProvider(provider) {
		return model.ErrIdentityNotFound
	}
	return s.repo.UnlinkIdentity(ctx, userID, provider)
}
