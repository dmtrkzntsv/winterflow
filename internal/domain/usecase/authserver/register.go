package authserver

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"winterflow/internal/domain/model"
	"winterflow/pkg/util"
)

const (
	defaultClientName = "MCP client"
	maxClientName     = 100
	maxRedirectURIs   = 10
	maxRedirectURILen = 2048
)

// Register is open dynamic client registration (RFC 7591); the route's
// per-IP limiter bounds it. A client that asks for a client_secret_* auth
// method gets a secret (returned once, stored hashed); the default is a
// public client.
func (u *UseCase) Register(ctx context.Context, req model.ClientRegistration) (model.RegisteredClient, error) {
	if !u.Enabled() {
		return model.RegisteredClient{}, errDisabled
	}
	name := strings.TrimSpace(strings.Map(dropInvisible, req.ClientName))
	if name == "" {
		name = defaultClientName
	}
	if utf8.RuneCountInString(name) > maxClientName {
		return model.RegisteredClient{}, invalidMetadata("client_name is too long")
	}
	if n := len(req.RedirectURIs); n < 1 || n > maxRedirectURIs {
		return model.RegisteredClient{}, &OAuthError{Status: 400, Code: "invalid_redirect_uri", Description: "between 1 and 10 redirect_uris are required"}
	}
	for _, uri := range req.RedirectURIs {
		if len(uri) > maxRedirectURILen {
			return model.RegisteredClient{}, &OAuthError{Status: 400, Code: "invalid_redirect_uri", Description: "redirect URI is too long"}
		}
		if err := ValidateRedirectURI(uri); err != nil {
			return model.RegisteredClient{}, &OAuthError{Status: 400, Code: "invalid_redirect_uri", Description: err.Error()}
		}
	}
	grantTypes := req.GrantTypes
	if len(grantTypes) == 0 {
		grantTypes = []string{"authorization_code", "refresh_token"}
	}
	for _, g := range grantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			return model.RegisteredClient{}, invalidMetadata("unsupported grant_types")
		}
	}
	responseTypes := req.ResponseTypes
	if len(responseTypes) == 0 {
		responseTypes = []string{"code"}
	}
	for _, r := range responseTypes {
		if r != "code" {
			return model.RegisteredClient{}, invalidMetadata("unsupported response_types")
		}
	}
	method := req.TokenEndpointAuthMethod
	if method == "" {
		method = "none"
	}
	if !slices.Contains([]string{"none", "client_secret_post", "client_secret_basic"}, method) {
		return model.RegisteredClient{}, invalidMetadata("unsupported token_endpoint_auth_method")
	}

	now := u.now().UTC()
	if _, err := u.repo.PurgeUnusedClients(ctx, now.Add(-UnusedClientTTL)); err != nil {
		slog.WarnContext(ctx, "authserver: purge unused clients failed", "error", err)
	}
	client := model.OAuthClient{ID: util.GenerateID(), Name: name, RedirectURIs: req.RedirectURIs, CreatedAt: now}
	res := model.RegisteredClient{
		ClientID:                client.ID,
		ClientIDIssuedAt:        now.Unix(),
		ClientName:              name,
		RedirectURIs:            req.RedirectURIs,
		GrantTypes:              grantTypes,
		ResponseTypes:           responseTypes,
		TokenEndpointAuthMethod: method,
	}
	if method != "none" {
		raw, hash, err := newSecret("")
		if err != nil {
			return model.RegisteredClient{}, err
		}
		client.SecretHash = &hash
		res.ClientSecret = raw
		never := int64(0)
		res.ClientSecretExpiresAt = &never
	}
	if err := u.repo.InsertClient(ctx, client); err != nil {
		return model.RegisteredClient{}, err
	}
	return res, nil
}

// dropInvisible drops control and format runes (newlines, bidi overrides),
// which would let a client name spoof or reflow the consent page.
func dropInvisible(r rune) rune {
	if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
		return -1
	}
	return r
}

func invalidMetadata(desc string) *OAuthError {
	return &OAuthError{Status: 400, Code: "invalid_client_metadata", Description: desc}
}
