package port

import (
	"context"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
)

type UserRepository interface {
	GetByConnectedAccount(ctx context.Context, provider, accountID string) (model.User, error)
	CreateUser(ctx context.Context, dto dto.UserDTO) (model.User, error)
	GetUser(ctx context.Context, userID string) (model.User, error)
	// PrimaryOrganizationID returns the organization the user belongs to.
	// In the 1-user-per-org model this is the org created at signup.
	PrimaryOrganizationID(ctx context.Context, userID string) (string, error)

	// FindByToken resolves a personal access token to its user, enforcing
	// expiry. Returns model.ErrInvalidToken when the token is unknown/expired.
	FindByToken(ctx context.Context, token string) (model.User, error)
	// CreateToken issues a personal access token for a user, returning the
	// generated token string.
	CreateToken(ctx context.Context, userID string) (string, error)
	// ListTokens returns the user's token metadata (without the secret).
	ListTokens(ctx context.Context, userID string) ([]model.Token, error)
	// RevokeToken deletes a token by id, scoped to the owning user.
	RevokeToken(ctx context.Context, userID, tokenID string) error
}

type UserService interface {
	FindOrCreateUser(ctx context.Context, dto dto.UserDTO) (model.User, error)
	PrimaryOrganizationID(ctx context.Context, userID string) (string, error)
	FindByToken(ctx context.Context, token string) (model.User, error)
}
