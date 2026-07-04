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

	// FindOrCreateUser resolves a connected account to a user, creating the
	// user + org on first login.
	FindOrCreateUser(ctx context.Context, dto dto.UserDTO) (model.User, error)
}

// UserService is the auth-facing slice of the user repository. The DB
// repository implements it directly; there is no separate service layer.
type UserService = UserRepository
