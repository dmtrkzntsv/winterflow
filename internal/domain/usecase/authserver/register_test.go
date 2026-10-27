package authserver

import (
	"errors"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/model"
)

func TestRegister(t *testing.T) {
	f := newFixture(t)
	res, err := f.s.Register(f.ctx, model.ClientRegistration{ClientName: "Claude", RedirectURIs: []string{callback}})
	if err != nil || res.ClientID == "" || res.ClientSecret != "" || res.TokenEndpointAuthMethod != "none" {
		t.Fatalf("%+v %v", res, err)
	}
	if len(res.GrantTypes) != 2 || res.ResponseTypes[0] != "code" {
		t.Fatalf("defaults: %+v", res)
	}
	conf, err := f.s.Register(f.ctx, model.ClientRegistration{RedirectURIs: []string{"https://a.test/cb"}, TokenEndpointAuthMethod: "client_secret_post"})
	if err != nil || conf.ClientSecret == "" || conf.ClientName != "MCP client" || conf.ClientSecretExpiresAt == nil || *conf.ClientSecretExpiresAt != 0 {
		t.Fatalf("confidential: %+v %v", conf, err)
	}
	for _, bad := range []model.ClientRegistration{
		{},
		{RedirectURIs: []string{"http://evil.test/cb"}},
		{RedirectURIs: []string{"https://a.test/cb"}, TokenEndpointAuthMethod: "private_key_jwt"},
		{RedirectURIs: []string{"https://a.test/cb"}, GrantTypes: []string{"client_credentials"}},
		{RedirectURIs: []string{"https://a.test/cb"}, ResponseTypes: []string{"token"}},
		{RedirectURIs: []string{"https://a.test/cb"}, ClientName: strings.Repeat("x", 101)},
		{RedirectURIs: []string{"https://a/1", "https://a/2", "https://a/3", "https://a/4", "https://a/5", "https://a/6", "https://a/7", "https://a/8", "https://a/9", "https://a/10", "https://a/11"}},
	} {
		var oe *OAuthError
		if _, err := f.s.Register(f.ctx, bad); !errors.As(err, &oe) || oe.Status != 400 {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestRegisterDisabled(t *testing.T) {
	f := newFixture(t)
	var oe *OAuthError
	if _, err := f.off().Register(f.ctx, model.ClientRegistration{RedirectURIs: []string{"https://a.test/cb"}}); !errors.As(err, &oe) || oe.Status != 404 {
		t.Fatalf("disabled: %v", err)
	}
}

func TestRegisterPurgesUnusedClients(t *testing.T) {
	f := newFixture(t)
	old := f.register(t, "", "https://a.test/cb")
	// A client that registers and only reaches the consent page days later
	// (a laptop asleep over a weekend) must still be known.
	f.clock.Advance(7 * 24 * time.Hour)
	f.register(t, "", "https://a.test/cb")
	q := authReq(old.ClientID)
	q.RedirectURI = "https://a.test/cb"
	if _, err := f.s.DescribeAuthorization(f.ctx, q); err != nil {
		t.Fatalf("a week-old unused client must survive the purge: %v", err)
	}
	f.clock.Advance(UnusedClientTTL)
	f.register(t, "", "https://a.test/cb")
	if _, err := f.s.DescribeAuthorization(f.ctx, q); !errors.Is(err, model.ErrOAuthClientUnknown) {
		t.Fatalf("an unused client must be purged: %v", err)
	}
}

func TestRegisterSanitizesAndCaps(t *testing.T) {
	f := newFixture(t)
	res, err := f.s.Register(f.ctx, model.ClientRegistration{ClientName: "Cla‮ude\n", RedirectURIs: []string{"https://a.test/cb"}})
	if err != nil || res.ClientName != "Claude" {
		t.Fatalf("name must lose control and bidi runes: %q %v", res.ClientName, err)
	}
	only, err := f.s.Register(f.ctx, model.ClientRegistration{ClientName: "‮\n", RedirectURIs: []string{"https://a.test/cb"}})
	if err != nil || only.ClientName != "MCP client" {
		t.Fatalf("a name of invisible runes only falls back to the default: %q %v", only.ClientName, err)
	}
	var oe *OAuthError
	long := "https://a.test/" + strings.Repeat("x", 2048)
	if _, err := f.s.Register(f.ctx, model.ClientRegistration{RedirectURIs: []string{long}}); !errors.As(err, &oe) || oe.Code != "invalid_redirect_uri" {
		t.Fatalf("long redirect URI: %v", err)
	}
	if _, err := f.s.Register(f.ctx, model.ClientRegistration{RedirectURIs: []string{"https://clаude.ai/cb"}}); !errors.As(err, &oe) || oe.Code != "invalid_redirect_uri" {
		t.Fatalf("homograph redirect URI: %v", err)
	}
}
