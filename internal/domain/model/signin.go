package model

import "time"

// Sign-in providers (Winterflow as an OAuth client). The id is the
// provider's slot: it names the identity rows, the routes
// (/auth/oauth/<id>/...) and the login page's button.
const (
	ProviderGoogle = "google"
	ProviderApple  = "apple"
	ProviderOIDC   = "oidc"
)

// SignInProvider is a configured provider as the login page lists it.
type SignInProvider struct {
	ID   string `json:"id" enum:"google,apple,oidc"`
	Name string `json:"name"`
}

// ProviderClaims is what a provider's verified ID token (or its userinfo
// document) says about the user.
type ProviderClaims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

// ProviderTokens is a provider's answer to the code exchange.
type ProviderTokens struct {
	IDToken     string
	AccessToken string
}

// Identity is a provider account linked to a user (a
// user_connected_accounts row): Subject is the provider's stable user id,
// Email the address it vouched for when the link was made or last used,
// Issuer the provider's issuer URL the subject was verified against ("" on
// rows that predate it).
type Identity struct {
	Provider  string
	Subject   string
	UserID    string
	Email     string
	Issuer    string
	CreatedAt time.Time
}

// SameIssuer reports whether a stored identity was verified against issuer.
// A subject is unique only per issuer, so an identity linked through another
// issuer (a provider slot repointed at another IdP) is not this account. A
// row with no issuer predates the column and can only be Google's or
// Apple's, whose issuer is fixed; a generic OIDC identity always has one.
func (id Identity) SameIssuer(issuer string) bool {
	if id.Issuer == "" {
		return id.Provider != ProviderOIDC
	}
	return id.Issuer == issuer
}

// Sign-in intents a login state carries.
const (
	IntentLogin = "login"
	IntentLink  = "link"
)

// LoginState is one in-flight authorization request (oauth_login_states),
// keyed by the SHA-256 of the state value sent to the provider. FlowHash
// is the SHA-256 of the secret the starting browser keeps in a cookie;
// LinkUserID names the session's user for a link.
type LoginState struct {
	StateHash    string
	Provider     string
	Nonce        string
	CodeVerifier string
	FlowHash     string
	Intent       string
	ReturnTo     string
	LinkUserID   string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// Sign-in and linked-identity errors.
var (
	// ErrProviderNotConfigured is a sign-in with a provider this instance
	// has not configured.
	ErrProviderNotConfigured = &classified{"sign-in provider is not configured", ErrNotFound}
	// ErrLoginStateNotFound is an unknown (or already consumed) state.
	ErrLoginStateNotFound = &classified{"sign-in request not found", ErrNotFound}
	// ErrIdentityNotFound is an unlink of a provider the user has not linked.
	ErrIdentityNotFound = &classified{"linked account not found", ErrNotFound}
	// ErrIdentityTaken is a provider account already linked to another user.
	ErrIdentityTaken = &classified{"this account is already linked to another user", ErrConflict}
	// ErrProviderAlreadyLinked is a second account of a provider the user
	// already linked.
	ErrProviderAlreadyLinked = &classified{"another account of this provider is already linked", ErrConflict}
	// ErrLastSignInMethod refuses an unlink that would leave the user with
	// no password and no linked provider.
	ErrLastSignInMethod = &classified{"set a password or link another provider before unlinking your only sign-in method", ErrConflict}
)
