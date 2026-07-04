package port

import (
	"context"
	"time"
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

	// CreateToken mints a PAT for the user. Returns the stored record and the
	// plaintext — the only time the plaintext ever exists outside the caller.
	CreateToken(ctx context.Context, userID, name string, expiresAt *time.Time) (model.UserToken, string, error)
	// ListTokens returns the user's tokens, newest first. Never plaintext.
	ListTokens(ctx context.Context, userID string) ([]model.UserToken, error)
	// DeleteToken removes the user's token. model.ErrTokenNotFound if the id
	// does not exist or belongs to another user.
	DeleteToken(ctx context.Context, userID, tokenID string) error

	// CountUsers reports how many users exist (0 = fresh instance, local
	// login bootstraps the admin).
	CountUsers(ctx context.Context) (int, error)
	// BootstrapLocalAdmin creates the first account (owner of a new org)
	// from the first local login. model.ErrNotBootstrap when users exist.
	BootstrapLocalAdmin(ctx context.Context, email, password string) (model.User, error)
	// VerifyLocalCredentials checks an email+password pair.
	// model.ErrInvalidCredentials on any mismatch.
	VerifyLocalCredentials(ctx context.Context, email, password string) (model.User, error)
	// CreateMemberUser provisions an account inside an existing org with a
	// must-change temp password. model.ErrEmailTaken on duplicates.
	CreateMemberUser(ctx context.Context, orgID, name, email, role, tempPassword string) (model.User, error)
	// SetPassword replaces the user's password hash and must-change flag.
	SetPassword(ctx context.Context, userID, password string, mustChange bool) error
	// GetCredentials returns the non-secret credential info (email,
	// must-change). model.ErrorUserNotFound for users without local creds.
	GetCredentials(ctx context.Context, userID string) (model.Credentials, error)
	// ListMembers returns the org's memberships joined with their users.
	ListMembers(ctx context.Context, orgID string) ([]model.Member, error)
	// UpdateMemberRole changes a member's role. model.ErrLastOwner guards
	// demoting the only owner.
	UpdateMemberRole(ctx context.Context, orgID, userID, role string) error
	// RemoveMember deletes the member's user row outright (cascades to
	// credentials, accounts, PATs). model.ErrLastOwner guards the only owner.
	RemoveMember(ctx context.Context, orgID, userID string) error
	// RoleOf returns the user's role in their primary organization.
	RoleOf(ctx context.Context, userID string) (string, error)

	// FindOrCreateUser resolves a connected account to a user, creating the
	// user + org on first login.
	FindOrCreateUser(ctx context.Context, dto dto.UserDTO) (model.User, error)
}

// UserService is the auth-facing slice of the user repository. The DB
// repository implements it directly; there is no separate service layer.
type UserService = UserRepository
