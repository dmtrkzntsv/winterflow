package oauthprovider_test

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"winterflow/internal/infra/oauthprovider"
	"winterflow/internal/infra/oauthprovider/oauthprovidertest"
)

func TestClient_AuthURLExchangeUserInfo(t *testing.T) {
	f := oauthprovidertest.New(t)
	f.RequirePKCE = true
	iss := f.Issuer("oidc", false)
	iss.ExtraAuthParams = map[string]string{"prompt": "select_account"}
	c := oauthprovider.NewClient(iss, nil)

	verifier, _ := oauthprovider.RandomToken()
	raw, err := c.AuthURL(context.Background(), "st", "no", oauthprovider.PKCEChallenge(verifier), "https://app.example.test/cb")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	if !strings.HasPrefix(raw, f.IssuerURL()+"/authorize?") || q.Get("response_type") != "code" || q.Get("client_id") != f.ClientID ||
		q.Get("redirect_uri") != "https://app.example.test/cb" || q.Get("scope") != "openid profile email" || q.Get("state") != "st" ||
		q.Get("nonce") != "no" || q.Get("code_challenge") != oauthprovider.PKCEChallenge(verifier) || q.Get("code_challenge_method") != "S256" ||
		q.Get("prompt") != "select_account" {
		t.Fatalf("auth url %s", raw)
	}

	code := f.IssueCode("no", q.Get("code_challenge"))
	toks, err := c.Exchange(context.Background(), code, verifier, "https://app.example.test/cb", time.Now())
	if err != nil || toks.IDToken == "" || toks.AccessToken == "" {
		t.Fatalf("exchange %+v %v", toks, err)
	}
	if _, err := c.Exchange(context.Background(), code, "wrong-verifier", "https://app.example.test/cb", time.Now()); err == nil {
		t.Fatal("wrong verifier must fail")
	}

	f.UserInfoEmail = "info@example.test"
	info, err := c.UserInfo(context.Background(), toks.AccessToken)
	if err != nil || info.Email != "info@example.test" || info.Subject != f.Subject {
		t.Fatalf("userinfo %+v %v", info, err)
	}

}

func TestClient_FormPostAndNoPKCE(t *testing.T) {
	f := oauthprovidertest.New(t)
	iss := f.Issuer("apple", true)
	iss.UsePKCE = false
	iss.ResponseMode = "form_post"
	iss.Scopes = []string{"name", "email"}
	c := oauthprovider.NewClient(iss, nil)
	raw, _ := c.AuthURL(context.Background(), "s", "n", "", "https://a/cb")
	u, _ := url.Parse(raw)
	if u.Query().Get("response_mode") != "form_post" || u.Query().Has("code_challenge") || u.Query().Get("scope") != "name email" {
		t.Fatalf("apple auth url %s", raw)
	}
}

func TestDiscover_RejectsIssuerMismatch(t *testing.T) {
	f := oauthprovidertest.New(t)
	f.DiscoveryIssuer = "https://evil.example.test"
	_, err := oauthprovider.NewClient(f.Issuer("oidc", false), nil).Discover(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not match the configured issuer") {
		t.Fatalf("a discovery document claiming another issuer must be rejected, got %v", err)
	}
}

// TestClient_ExchangeTokenAuthMethod: the exchange authenticates the way the
// issuer advertises (client_secret_basic when listed or when nothing is
// listed, else client_secret_post) unless the issuer config pins a method.
func TestClient_ExchangeTokenAuthMethod(t *testing.T) {
	cases := []struct {
		name       string
		advertised []string
		pinned     string
		want       string
	}{
		{"nothing advertised", nil, "", oauthprovider.AuthClientSecretBasic},
		{"basic only", []string{"client_secret_basic"}, "", oauthprovider.AuthClientSecretBasic},
		{"both", []string{"client_secret_post", "client_secret_basic"}, "", oauthprovider.AuthClientSecretBasic},
		{"post only", []string{"private_key_jwt", "client_secret_post"}, "", oauthprovider.AuthClientSecretPost},
		{"pinned post", []string{"client_secret_post", "client_secret_basic"}, oauthprovider.AuthClientSecretPost, oauthprovider.AuthClientSecretPost},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := oauthprovidertest.New(t)
			f.TokenAuthMethods = tc.advertised
			// A secret with characters RFC 6749's form-encoding of the
			// Basic credentials changes.
			f.Secret = "s3cr:et +/%"
			iss := f.Issuer("oidc", false)
			iss.TokenAuthMethod = tc.pinned
			c := oauthprovider.NewClient(iss, nil)
			verifier, _ := oauthprovider.RandomToken()
			code := f.IssueCode("n", oauthprovider.PKCEChallenge(verifier))
			toks, err := c.Exchange(context.Background(), code, verifier, "https://app.example.test/cb", time.Now())
			if err != nil || toks.IDToken == "" {
				t.Fatalf("exchange %+v %v", toks, err)
			}
			if got := f.TokenAuthUsed(); got != tc.want {
				t.Fatalf("authenticated with %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClient_ExchangeRefusesIssuerWithoutSecretMethod(t *testing.T) {
	f := oauthprovidertest.New(t)
	f.TokenAuthMethods = []string{"private_key_jwt"}
	c := oauthprovider.NewClient(f.Issuer("oidc", false), nil)
	code := f.IssueCode("n", "")
	_, err := c.Exchange(context.Background(), code, "v", "https://app.example.test/cb", time.Now())
	if err == nil || !strings.Contains(err.Error(), "neither") {
		t.Fatalf("err = %v, want a refusal naming the methods", err)
	}
	if got := f.TokenAuthUsed(); got != "" {
		t.Fatalf("a token request was sent (%q)", got)
	}
}
