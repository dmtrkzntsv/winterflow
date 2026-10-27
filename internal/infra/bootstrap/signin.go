package bootstrap

import (
	"net/http"
	"os"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/oauthlogin"
	oauthloginrepo "winterflow/internal/infra/db/repository/oauthlogin"
	userrepo "winterflow/internal/infra/db/repository/user"
	"winterflow/internal/infra/oauthprovider"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

const (
	// googleIssuerURL is Google's OpenID issuer.
	googleIssuerURL = "https://accounts.google.com"
	// providerHTTPTimeout bounds every request to a provider (discovery,
	// JWKS, token, userinfo).
	providerHTTPTimeout = 10 * time.Second
)

// WithSignInProviders replaces the sign-in providers the configuration
// names (tests: a fake OpenID provider), with origin as the public origin
// the redirect URIs are built on.
func WithSignInProviders(origin string, providers ...oauthlogin.Provider) CoreOption {
	return func(o *coreOptions) {
		o.signInOrigin = origin
		o.signInProviders = providers
		o.signInSet = true
	}
}

// newSignIns builds the provider sign-in usecase over the configured
// providers.
func newSignIns(repo *oauthloginrepo.Repo, users *userrepo.Repo, o coreOptions, cfg *config.ServerConfig, log *logger.Logger) *oauthlogin.Service {
	origin, providers := o.signInOrigin, o.signInProviders
	if !o.signInSet {
		origin, providers = signInProvidersFromConfig(cfg, log)
	}
	return oauthlogin.New(providers, repo, users, origin, nil, log)
}

// signInProvidersFromConfig builds the providers whose variables are all
// set, in display order (Google, Apple, the generic OIDC slot). They need
// the instance's public origin for their redirect URIs: without one (WEB_URL
// not an https origin, nor http on loopback) none is enabled. A provider
// that cannot be built (an unreadable Apple key) is left out with an error
// in the log, so password sign-in keeps working.
func signInProvidersFromConfig(cfg *config.ServerConfig, log *logger.Logger) (string, []oauthlogin.Provider) {
	wanted := cfg.IsAuthSupported(model.ProviderGoogle) || cfg.IsAuthSupported(model.ProviderApple) ||
		cfg.IsAuthSupported(model.ProviderOIDC)
	if !wanted {
		return "", nil
	}
	origin, ok := cfg.GetPublicOrigin()
	if !ok {
		log.Error("sign-in providers disabled: WEB_URL must be an https origin (or http on localhost) for their redirect URIs",
			"web_url", cfg.GetWebURL())
		return "", nil
	}
	hc := &http.Client{Timeout: providerHTTPTimeout}
	var out []oauthlogin.Provider
	if cfg.IsAuthSupported(model.ProviderGoogle) {
		id, secret := cfg.GetGoogleAuth()
		out = append(out, oauthlogin.Provider{ID: model.ProviderGoogle, Name: "Google", Client: oauthprovider.NewClient(oauthprovider.Issuer{
			ID: model.ProviderGoogle, IssuerURL: googleIssuerURL, ClientID: id, ClientSecret: oauthprovider.StaticSecret(secret),
			Scopes: []string{"openid", "email", "profile"}, UsePKCE: true,
			// Google sends email_verified (false for an unverified address).
			TrustEmail:      false,
			ExtraAuthParams: map[string]string{"prompt": "select_account"},
			IssuerAliases:   []string{"accounts.google.com"},
		}, hc)})
	}
	if cfg.IsAuthSupported(model.ProviderApple) {
		if p, err := appleProvider(cfg.GetAppleAuth(), hc); err != nil {
			log.Error("Sign in with Apple disabled: AUTH_APPLE_PRIVATE_KEY_PATH", "error", err)
		} else {
			out = append(out, p)
		}
	}
	if cfg.IsAuthSupported(model.ProviderOIDC) {
		o := cfg.GetOIDCAuth()
		out = append(out, oauthlogin.Provider{ID: model.ProviderOIDC, Name: o.Name, Client: oauthprovider.NewClient(oauthprovider.Issuer{
			ID: model.ProviderOIDC, IssuerURL: o.Issuer, ClientID: o.ClientID, ClientSecret: oauthprovider.StaticSecret(o.ClientSecret),
			Scopes: []string{"openid", "email", "profile"}, UsePKCE: true, TrustEmail: false,
		}, hc)})
	}
	return origin, out
}

// appleProvider reads the .p8 key once; the client secret is a fresh ES256
// JWT signed with it at every exchange.
func appleProvider(a config.AppleAuth, hc *http.Client) (oauthlogin.Provider, error) {
	pem, err := os.ReadFile(a.PrivateKeyPath)
	if err != nil {
		return oauthlogin.Provider{}, err
	}
	key, err := oauthprovider.ParseApplePrivateKey(string(pem))
	if err != nil {
		return oauthlogin.Provider{}, err
	}
	teamID, clientID, keyID := a.TeamID, a.ClientID, a.KeyID
	return oauthlogin.Provider{ID: model.ProviderApple, Name: "Apple", Client: oauthprovider.NewClient(oauthprovider.Issuer{
		ID: model.ProviderApple, IssuerURL: oauthprovider.AppleIssuerURL, ClientID: clientID,
		ClientSecret: func(now time.Time) (string, error) {
			return oauthprovider.AppleClientSecret(teamID, clientID, keyID, key, now)
		},
		// Apple returns the name and email only with form_post; PKCE is
		// undocumented there: the confidential client secret and the nonce
		// bind the exchange. Apple vouches for the addresses it relays.
		Scopes: []string{"name", "email"}, UsePKCE: false, ResponseMode: "form_post", TrustEmail: true,
	}, hc)}, nil
}
