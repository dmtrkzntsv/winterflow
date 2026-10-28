package port

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
)

// SignInRepository stores linked identities (user_connected_accounts,
// without the email+password accounts' "local" rows) and the in-flight
// provider sign-ins (oauth_login_states).
type SignInRepository interface {
	// IdentityBySubject finds a linked provider account;
	// model.ErrIdentityNotFound when none.
	IdentityBySubject(ctx context.Context, provider, subject string) (model.Identity, error)
	// Identities lists the user's linked provider accounts, oldest first.
	Identities(ctx context.Context, userID string) ([]model.Identity, error)
	// UpdateIdentityEmail records the address a provider vouched for at the
	// latest sign-in.
	UpdateIdentityEmail(ctx context.Context, provider, subject, email string) error
	// RekeyIdentity moves the row keyed legacyID to subject, reporting
	// whether there was one.
	RekeyIdentity(ctx context.Context, provider, legacyID, subject string) (bool, error)
	// LinkIdentity attaches a provider account to id.UserID, under the
	// user's row lock: model.ErrIdentityTaken when it belongs to another
	// user, model.ErrProviderAlreadyLinked when the user already has
	// another account of that provider. Linking one the user already has
	// refreshes its email; one stored under another issuer is
	// model.ErrIdentityTaken.
	LinkIdentity(ctx context.Context, id model.Identity) error
	// UnlinkIdentity removes the user's account of provider, under the
	// user's row lock: model.ErrIdentityNotFound when there is none,
	// model.ErrLastSignInMethod when it is the passwordless user's only one.
	UnlinkIdentity(ctx context.Context, userID, provider string) error
	// EmailInUse reports whether an account answers to email already (a
	// password account's address or a linked provider's).
	EmailInUse(ctx context.Context, email string) (bool, error)

	InsertLoginState(ctx context.Context, s model.LoginState) error
	// ConsumeLoginState deletes and returns a state:
	// model.ErrLoginStateNotFound when it is unknown or already consumed.
	ConsumeLoginState(ctx context.Context, stateHash string) (model.LoginState, error)
	// DeleteExpiredLoginStates drops the states that expired before now.
	DeleteExpiredLoginStates(ctx context.Context, now time.Time) error
}
