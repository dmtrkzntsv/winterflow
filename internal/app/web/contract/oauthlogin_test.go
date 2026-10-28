package contract_test

import (
	"net/http"
	"net/url"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/oauthlogin"
	"winterflow/internal/infra/bootstrap"
	"winterflow/internal/infra/oauthprovider"
	"winterflow/internal/infra/oauthprovider/oauthprovidertest"
)

// newSignInEnv is newEnv with a fake OpenID provider in the "oidc" slot.
func newSignInEnv(t *testing.T) (*env, *oauthprovidertest.Fake) {
	t.Helper()
	f := oauthprovidertest.New(t)
	e := newEnv(t, bootstrap.WithSignInProviders("http://localhost", oauthlogin.Provider{
		ID: model.ProviderOIDC, Name: "Test SSO", Client: oauthprovider.NewClient(f.Issuer(model.ProviderOIDC, false), nil),
	}))
	return e, f
}

// providerSignIn runs a provider flow as c: start (with q), the provider's
// consent (the fake issues a code), then the callback. It returns the
// callback's answer.
func (c *client) providerSignIn(t *testing.T, f *oauthprovidertest.Fake, q url.Values) *response {
	t.Helper()
	start := c.get(t, "/auth/oauth/oidc/start", q).expect(t, http.StatusFound)
	at, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	aq := at.Query()
	code := f.IssueCode(aq.Get("nonce"), aq.Get("code_challenge"))
	return c.get(t, "/auth/oauth/oidc/callback", url.Values{"code": {code}, "state": {aq.Get("state")}}).expect(t, http.StatusFound)
}

type identityItem struct {
	Provider string `json:"provider"`
	Email    string `json:"email"`
}

func (c *client) identities(t *testing.T) []identityItem {
	t.Helper()
	var ids []identityItem
	c.get(t, "/api/v1/user/get-identities", nil).expect(t, http.StatusOK).data(t, &ids)
	return ids
}

// TestProviderSignUpClaimsInstance: on a fresh instance the first provider
// sign-in creates the owner (and, in standalone, claims the server); its
// only sign-in method cannot be unlinked.
func TestProviderSignUpClaimsInstance(t *testing.T) {
	t.Parallel()
	e, f := newSignInEnv(t)
	c := e.anon()

	var st struct {
		Providers []model.SignInProvider `json:"providers"`
	}
	c.get(t, "/api/v1/auth/state", nil).expect(t, http.StatusOK).data(t, &st)
	if len(st.Providers) != 1 || st.Providers[0].ID != model.ProviderOIDC || st.Providers[0].Name != "Test SSO" {
		t.Fatalf("providers = %+v", st.Providers)
	}

	r := c.providerSignIn(t, f, url.Values{"return_to": {"/apps"}})
	if loc := r.Header.Get("Location"); loc != "/apps" {
		t.Fatalf("callback redirect = %q, want /apps", loc)
	}
	if sessionCookie(r) == nil {
		t.Fatal("the callback set no session cookie")
	}
	if id := c.serverID(t); id == "" {
		t.Fatal("no server after the provider sign-up")
	}
	ids := c.identities(t)
	if len(ids) != 1 || ids[0].Provider != model.ProviderOIDC || ids[0].Email != f.Email {
		t.Fatalf("identities = %+v", ids)
	}
	c.post(t, "/api/v1/user/unlink-identity", map[string]string{"provider": "oidc"}).expect(t, http.StatusConflict)
	c.post(t, "/api/v1/user/unlink-identity", map[string]string{"provider": "google"}).expect(t, http.StatusNotFound)
}

// TestProviderLinkSignInUnlink: a password account links a provider, signs
// in with it, and unlinks it again.
func TestProviderLinkSignInUnlink(t *testing.T) {
	t.Parallel()
	e, f := newSignInEnv(t)
	owner := e.claim(t)

	r := owner.providerSignIn(t, f, url.Values{"mode": {"link"}, "return_to": {"/settings"}})
	if loc := r.Header.Get("Location"); loc != "/settings" {
		t.Fatalf("link redirect = %q, want /settings", loc)
	}
	if ids := owner.identities(t); len(ids) != 1 || ids[0].Provider != model.ProviderOIDC {
		t.Fatalf("identities after the link = %+v", ids)
	}

	// The linked provider now signs the owner in.
	other := e.anon()
	other.providerSignIn(t, f, nil)
	var prof struct {
		ID string `json:"user_id"`
	}
	other.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusOK).data(t, &prof)
	if prof.ID != owner.userID {
		t.Fatalf("provider sign-in reached user %q, want the owner %q", prof.ID, owner.userID)
	}

	owner.post(t, "/api/v1/user/unlink-identity", map[string]string{"provider": "oidc"}).expect(t, http.StatusOK)
	if ids := owner.identities(t); len(ids) != 0 {
		t.Fatalf("identities after the unlink = %+v", ids)
	}
	// Standalone is closed once claimed, and the address is the owner's
	// anyway: the unlinked provider account signs nobody in.
	r = e.anon().providerSignIn(t, f, nil)
	if loc := r.Header.Get("Location"); loc != "/login?error=registration_closed" {
		t.Fatalf("sign-in after the unlink redirected to %q", loc)
	}
}

// TestAppleFormPostResends: a form_post answer goes back to the GET
// callback, where the browser's SameSite=Lax cookies come along.
func TestAppleFormPostResends(t *testing.T) {
	t.Parallel()
	e, _ := newSignInEnv(t)
	r := e.anon().do(t, call{Method: http.MethodPost, Path: "/auth/oauth/oidc/callback",
		Body: "code=c1&state=s1&user=%7B%7D", Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}},
	}).expect(t, http.StatusSeeOther)
	loc, err := url.Parse(r.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := loc.Query()
	if loc.Path != "/auth/oauth/oidc/callback" || q.Get("code") != "c1" || q.Get("state") != "s1" || q.Get("user") != "{}" {
		t.Fatalf("form_post re-sent to %s", loc)
	}
}

// TestProviderAccountSetsPassword: an owner who signed up through a
// provider adds a password for the address it vouched for, without a
// current one, and can then sign in with it (a fallback for a provider
// that stops working); a second set, or one by a password account, is a
// conflict, and change-password still wants the current password.
func TestProviderAccountSetsPassword(t *testing.T) {
	t.Parallel()
	e, f := newSignInEnv(t)
	c := e.anon()
	c.providerSignIn(t, f, nil)
	var p struct {
		Email string `json:"email"`
	}
	c.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusOK).data(t, &p)
	if p.Email != "" {
		t.Fatalf("a provider account's profile email = %q, want none", p.Email)
	}
	c.post(t, "/api/v1/user/change-password", map[string]string{"current_password": "", "new_password": "fallback-pass"}).
		expect(t, http.StatusBadRequest)
	c.post(t, "/api/v1/user/set-password", map[string]string{"email": "someone-else@example.test", "new_password": "fallback-pass"}).
		expect(t, http.StatusBadRequest)
	c.post(t, "/api/v1/user/set-password", map[string]string{"email": f.Email, "new_password": "fallback-pass"}).
		expect(t, http.StatusOK)
	c.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusOK).data(t, &p)
	if p.Email != f.Email {
		t.Fatalf("profile email after set-password = %q, want %q", p.Email, f.Email)
	}
	e.anon().login(t, f.Email, "fallback-pass")
	c.post(t, "/api/v1/user/set-password", map[string]string{"email": f.Email, "new_password": "another-pass"}).
		expect(t, http.StatusConflict)
	c.post(t, "/api/v1/user/change-password", map[string]string{"current_password": "wrong", "new_password": "another-pass"}).
		expect(t, http.StatusBadRequest)
	c.post(t, "/api/v1/user/change-password", map[string]string{"current_password": "fallback-pass", "new_password": "another-pass"}).
		expect(t, http.StatusOK)
	e.anon().login(t, f.Email, "another-pass")
}
