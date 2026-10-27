package oauthprovider_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"winterflow/internal/infra/oauthprovider"
	"winterflow/internal/infra/oauthprovider/oauthprovidertest"
)

func discoveryServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func issuerFor(url string) oauthprovider.Issuer {
	return oauthprovider.Issuer{IssuerURL: url, ClientID: "cid", ClientSecret: oauthprovider.StaticSecret("secret"), Scopes: []string{"openid"}, UsePKCE: true}
}

func TestDiscover_Errors(t *testing.T) {
	ctx := context.Background()

	badStatus := discoveryServer(t, `{}`, http.StatusInternalServerError)
	if _, err := oauthprovider.NewClient(issuerFor(badStatus.URL), nil).Discover(ctx); err == nil {
		t.Error("non-200 discovery must fail")
	}

	badJSON := discoveryServer(t, `not json`, http.StatusOK)
	if _, err := oauthprovider.NewClient(issuerFor(badJSON.URL), nil).Discover(ctx); err == nil {
		t.Error("malformed discovery JSON must fail")
	}

	missingFields := discoveryServer(t, `{"issuer":"x"}`, http.StatusOK)
	if _, err := oauthprovider.NewClient(issuerFor(missingFields.URL), nil).Discover(ctx); err == nil {
		t.Error("discovery missing endpoints must fail")
	}

	unreachable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	unreachable.Close()
	if _, err := oauthprovider.NewClient(issuerFor(unreachable.URL), nil).Discover(ctx); err == nil {
		t.Error("unreachable issuer must fail")
	}

	if _, err := oauthprovider.NewClient(issuerFor("http://example.test/\ninvalid"), nil).Discover(ctx); err == nil {
		t.Error("invalid issuer URL must fail")
	}
}

func TestAuthURL_DiscoverErrorAndQuerySeparator(t *testing.T) {
	ctx := context.Background()
	badStatus := discoveryServer(t, `{}`, http.StatusInternalServerError)
	if _, err := oauthprovider.NewClient(issuerFor(badStatus.URL), nil).AuthURL(ctx, "s", "n", "c", "https://a/cb"); err == nil {
		t.Error("discover error must propagate")
	}

	withQuery := discoveryServer(t, "", http.StatusOK)
	// Replace with a body carrying an authorization_endpoint that already has a query string.
	withQuery.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"issuer":"` + withQuery.URL + `","authorization_endpoint":"` + withQuery.URL + `/authorize?foo=bar","token_endpoint":"` + withQuery.URL + `/token","jwks_uri":"` + withQuery.URL + `/jwks"}`))
	})
	raw, err := oauthprovider.NewClient(issuerFor(withQuery.URL), nil).AuthURL(ctx, "s", "n", "c", "https://a/cb")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "?foo=bar&") {
		t.Fatalf("expected '&' separator, got %s", raw)
	}
}

func TestExchange_Errors(t *testing.T) {
	ctx := context.Background()

	badStatus := discoveryServer(t, `{}`, http.StatusInternalServerError)
	if _, err := oauthprovider.NewClient(issuerFor(badStatus.URL), nil).Exchange(ctx, "code", "v", "https://a/cb", time.Now()); err == nil {
		t.Error("discover error must propagate")
	}

	good := discoveryServer(t, "", http.StatusOK)
	good.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_, _ = w.Write([]byte(`{"issuer":"` + good.URL + `","authorization_endpoint":"` + good.URL + `/authorize","token_endpoint":"` + good.URL + `/token","jwks_uri":"` + good.URL + `/jwks"}`))
		case "/token":
			_, _ = w.Write([]byte(`not json`))
		}
	})
	iss := issuerFor(good.URL)
	if _, err := oauthprovider.NewClient(iss, nil).Exchange(ctx, "code", "v", "https://a/cb", time.Now()); err == nil {
		t.Error("malformed token response must fail")
	}

	failingSecret := issuerFor(good.URL)
	failingSecret.ClientSecret = func(time.Time) (string, error) { return "", errInjected }
	if _, err := oauthprovider.NewClient(failingSecret, nil).Exchange(ctx, "code", "v", "https://a/cb", time.Now()); err == nil {
		t.Error("client secret error must propagate")
	}

	noIDToken := discoveryServer(t, "", http.StatusOK)
	noIDToken.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_, _ = w.Write([]byte(`{"issuer":"` + noIDToken.URL + `","authorization_endpoint":"` + noIDToken.URL + `/authorize","token_endpoint":"` + noIDToken.URL + `/token","jwks_uri":"` + noIDToken.URL + `/jwks"}`))
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"at"}`))
		}
	})
	if _, err := oauthprovider.NewClient(issuerFor(noIDToken.URL), nil).Exchange(ctx, "code", "v", "https://a/cb", time.Now()); err == nil {
		t.Error("missing id_token must fail")
	}

	badTokenURL := discoveryServer(t, "", http.StatusOK)
	badTokenURL.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"issuer":"` + badTokenURL.URL + `","authorization_endpoint":"` + badTokenURL.URL + `/authorize","token_endpoint":"http://example.test/\ninvalid","jwks_uri":"` + badTokenURL.URL + `/jwks"}`))
	})
	if _, err := oauthprovider.NewClient(issuerFor(badTokenURL.URL), nil).Exchange(ctx, "code", "v", "https://a/cb", time.Now()); err == nil {
		t.Error("invalid token endpoint URL must fail")
	}
}

var errInjected = errTest("injected")

type errTest string

func (e errTest) Error() string { return string(e) }

func TestUserInfo_Errors(t *testing.T) {
	ctx := context.Background()

	badStatus := discoveryServer(t, `{}`, http.StatusInternalServerError)
	if _, err := oauthprovider.NewClient(issuerFor(badStatus.URL), nil).UserInfo(ctx, "at"); err == nil {
		t.Error("discover error must propagate")
	}

	noUserInfo := discoveryServer(t, "", http.StatusOK)
	noUserInfo.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"issuer":"` + noUserInfo.URL + `","authorization_endpoint":"` + noUserInfo.URL + `/authorize","token_endpoint":"` + noUserInfo.URL + `/token","jwks_uri":"` + noUserInfo.URL + `/jwks"}`))
	})
	if _, err := oauthprovider.NewClient(issuerFor(noUserInfo.URL), nil).UserInfo(ctx, "at"); err == nil {
		t.Error("missing userinfo endpoint must fail")
	}

	unauthorized := discoveryServer(t, "", http.StatusOK)
	unauthorized.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_, _ = w.Write([]byte(`{"issuer":"` + unauthorized.URL + `","authorization_endpoint":"` + unauthorized.URL + `/authorize","token_endpoint":"` + unauthorized.URL + `/token","jwks_uri":"` + unauthorized.URL + `/jwks","userinfo_endpoint":"` + unauthorized.URL + `/userinfo"}`))
		case "/userinfo":
			w.WriteHeader(http.StatusUnauthorized)
		}
	})
	if _, err := oauthprovider.NewClient(issuerFor(unauthorized.URL), nil).UserInfo(ctx, "at"); err == nil {
		t.Error("non-200 userinfo must fail")
	}

	badJSON := discoveryServer(t, "", http.StatusOK)
	badJSON.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_, _ = w.Write([]byte(`{"issuer":"` + badJSON.URL + `","authorization_endpoint":"` + badJSON.URL + `/authorize","token_endpoint":"` + badJSON.URL + `/token","jwks_uri":"` + badJSON.URL + `/jwks","userinfo_endpoint":"` + badJSON.URL + `/userinfo"}`))
		case "/userinfo":
			_, _ = w.Write([]byte(`not json`))
		}
	})
	if _, err := oauthprovider.NewClient(issuerFor(badJSON.URL), nil).UserInfo(ctx, "at"); err == nil {
		t.Error("malformed userinfo JSON must fail")
	}
}

func TestClient_IssuerGetter(t *testing.T) {
	c := oauthprovider.NewClient(issuerFor("https://issuer.example.test/"), nil)
	if c.Issuer().IssuerURL != "https://issuer.example.test" {
		t.Fatalf("Issuer() = %+v", c.Issuer())
	}
}

func TestDiscover_RejectsNonHTTPSEndpoints(t *testing.T) {
	cases := map[string]func(*oauthprovidertest.Fake){
		"http token endpoint":      func(f *oauthprovidertest.Fake) { f.TokenEndpointOverride = "http://idp.example.test/token" },
		"javascript authorization": func(f *oauthprovidertest.Fake) { f.AuthorizationEndpointOverride = "javascript:alert(1)" },
		"relative jwks":            func(f *oauthprovidertest.Fake) { f.JWKSOverride = "/jwks" },
		"http userinfo":            func(f *oauthprovidertest.Fake) { f.UserInfoOverride = "http://idp.example.test/userinfo" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := oauthprovidertest.New(t)
			f.PublicURL = "https://idp.example.test"
			mutate(f)
			c := oauthprovider.NewClient(f.Issuer("oidc", false), &http.Client{Transport: f.Transport()})
			if _, err := c.Discover(context.Background()); err == nil {
				t.Fatal("discovery accepted an insecure endpoint")
			}
		})
	}
}

func TestDiscover_AllowsLoopbackHTTP(t *testing.T) {
	f := oauthprovidertest.New(t) // PublicURL unset: http://127.0.0.1:<port>
	if _, err := oauthprovider.NewClient(f.Issuer("oidc", false), f.Server.Client()).Discover(context.Background()); err != nil {
		t.Fatalf("loopback http must stay allowed for development: %v", err)
	}
}

func TestDiscover_RefusesAPlaintextIssuerBeforeAnyRequest(t *testing.T) {
	var hits int
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hits++
		return nil, http.ErrHandlerTimeout
	})}
	for _, iss := range []string{"http://id.example.com", "http://10.0.0.5/realms/x", "ftp://id.example.com", "https://u:p@id.example.com", "https://id.example.com?x=1"} {
		_, err := oauthprovider.NewClient(issuerFor(iss), hc).Discover(context.Background())
		if err == nil {
			t.Errorf("issuer %q: discovery must be refused", iss)
		}
	}
	if hits != 0 {
		t.Fatalf("a refused issuer must not be fetched, got %d requests", hits)
	}
}

func TestValidateIssuerURL(t *testing.T) {
	for _, ok := range []string{"https://id.example.com", "https://id.example.com/realms/home", "http://localhost:9000", "http://[::1]:9000/x"} {
		if err := oauthprovider.ValidateIssuerURL(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "id.example.com", "http://id.example.com", "http://localhost.evil.com", "https://id.example.com#f"} {
		if err := oauthprovider.ValidateIssuerURL(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestDiscover_RefusesARedirectToPlainHTTP(t *testing.T) {
	// The issuer itself is loopback http (allowed); it redirects off loopback
	// over plain http, which must not be followed.
	var followed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://id.example.com/.well-known/openid-configuration", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	base := http.DefaultTransport
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "id.example.com" {
			followed = true
			return nil, http.ErrHandlerTimeout
		}
		return base.RoundTrip(r)
	})}
	if _, err := oauthprovider.NewClient(issuerFor(srv.URL), hc).Discover(context.Background()); err == nil || !strings.Contains(err.Error(), "refusing a redirect") {
		t.Fatalf("want a refused redirect, got %v", err)
	}
	if followed {
		t.Fatal("the plaintext redirect was followed")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
