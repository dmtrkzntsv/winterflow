package oauthlogin

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
)

// Client is one provider's protocol client (internal/infra/oauthprovider):
// the authorization request, the code exchange and ID-token verification.
type Client interface {
	// UsesPKCE reports whether the authorization request carries a PKCE
	// challenge.
	UsesPKCE() bool
	// TrustsEmail reports whether the email claim counts as verified
	// without an email_verified claim saying so.
	TrustsEmail() bool
	AuthURL(ctx context.Context, state, nonce, codeChallenge, redirectURI string) (string, error)
	Exchange(ctx context.Context, code, codeVerifier, redirectURI string, now time.Time) (model.ProviderTokens, error)
	// VerifyIDToken checks the signature, issuer, audience, expiry and nonce.
	VerifyIDToken(ctx context.Context, raw, nonce string, now time.Time) (model.ProviderClaims, error)
	UserInfo(ctx context.Context, accessToken string) (model.ProviderClaims, error)
}

// Provider is one configured sign-in provider: its slot id
// (model.ProviderGoogle, ProviderApple, ProviderOIDC), the button label and
// the protocol client.
type Provider struct {
	ID     string
	Name   string
	Client Client
}

// knownProvider reports a provider slot id, configured or not (an identity
// linked while a provider was configured can still be listed and unlinked
// after it is switched off).
func knownProvider(id string) bool {
	switch id {
	case model.ProviderGoogle, model.ProviderApple, model.ProviderOIDC:
		return true
	}
	return false
}
