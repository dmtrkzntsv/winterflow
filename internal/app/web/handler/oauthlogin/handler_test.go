package oauthlogin_test

import (
	"context"
	"crypto/sha1" //nolint:gosec // the previous auth library's account keys
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"winterflow/internal/app/web/api"
	hauth "winterflow/internal/app/web/handler/auth"
	hoauthlogin "winterflow/internal/app/web/handler/oauthlogin"
	"winterflow/internal/app/web/middleware/authn"
	"winterflow/internal/domain/model"
	authnuc "winterflow/internal/domain/usecase/authn"
	"winterflow/internal/domain/usecase/oauthlogin"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	accesstokenrepo "winterflow/internal/infra/db/repository/accesstoken"
	oauthloginrepo "winterflow/internal/infra/db/repository/oauthlogin"
	userrepo "winterflow/internal/infra/db/repository/user"
	"winterflow/internal/infra/oauthprovider"
	"winterflow/internal/infra/oauthprovider/oauthprovidertest"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

var testLog = logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "oauthlogin-test"})

// harness is the sign-in routes over a real database, the real usecases and
// a fake OpenID provider in the given slot.
type harness struct {
	t      *testing.T
	ctx    context.Context
	d      *db.DB
	fake   *oauthprovidertest.Fake
	slot   string
	users  *userrepo.Repo
	signIn *oauthloginrepo.Repo
	tokens *authnuc.UseCase
	srv    *httptest.Server
	// open and firstOnly are the registration policy the callback sees.
	open, firstOnly bool
}

func newHarness(t *testing.T, slot string) *harness {
	t.Helper()
	h := &harness{t: t, ctx: dbtest.Ctx(t), d: dbtest.Open(t), fake: oauthprovidertest.New(t), slot: slot}
	var err error
	if h.users, err = userrepo.New(h.d.Tx, h.d.Driver, testLog); err != nil {
		t.Fatal(err)
	}
	if h.signIn, err = oauthloginrepo.New(h.d.Tx, h.d.Driver); err != nil {
		t.Fatal(err)
	}
	tokenRepo, err := accesstokenrepo.New(h.d.Tx, h.d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	h.tokens = authnuc.New(tokenRepo, nil)
	svc := oauthlogin.New([]oauthlogin.Provider{{
		ID: slot, Name: "Test", Client: oauthprovider.NewClient(h.fake.Issuer(slot, false), nil),
	}}, h.signIn, h.users, "http://localhost", nil, testLog)
	authAPI := hauth.NewHandler(&hauth.Deps{Logger: testLog, Cfg: config.NewServerConfig("standalone"), Users: h.users, Sessions: h.tokens})
	handler := hoauthlogin.NewHandler(&hoauthlogin.Deps{
		Logger: testLog, SignIns: svc, Sessions: authAPI, Auth: h.tokens,
		Registration: func(context.Context) (bool, bool) { return h.open, h.firstOnly },
	})
	pass := func(next http.Handler) http.Handler { return next }
	r := chi.NewRouter()
	api.Register(r, handler.Routes(api.Guards{AuthLimit: pass, EnrollLimit: pass, EnrollStartLimit: pass}),
		authn.Middleware(h.tokens, authn.Options{}))
	h.srv = httptest.NewServer(r)
	t.Cleanup(h.srv.Close)
	return h
}

// client is a browser: a cookie jar and no redirect following.
func (h *harness) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// signedIn is a browser holding a session of userID.
func (h *harness) signedIn(userID string) *http.Client {
	h.t.Helper()
	c := h.client()
	raw, err := h.tokens.IssueSession(h.ctx, userID, "test")
	if err != nil {
		h.t.Fatal(err)
	}
	u, _ := url.Parse(h.srv.URL)
	c.Jar.SetCookies(u, []*http.Cookie{{Name: authn.CookieName, Value: raw, Path: "/"}})
	return c
}

func (h *harness) do(c *http.Client, method, path string, q url.Values, body string) *http.Response {
	h.t.Helper()
	u := h.srv.URL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(h.ctx, method, u, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(authn.CSRFHeader, "1")
	}
	resp, err := c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// start begins a flow as c and returns the provider's authorization
// request parameters.
func (h *harness) start(c *http.Client, q url.Values) url.Values {
	h.t.Helper()
	resp := h.do(c, http.MethodGet, "/auth/oauth/"+h.slot+"/start", q, "")
	if resp.StatusCode != http.StatusFound {
		h.t.Fatalf("start = %d, want 302", resp.StatusCode)
	}
	at, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		h.t.Fatal(err)
	}
	return at.Query()
}

// callback answers an authorization request the way the provider would
// after consent, as c, and returns the callback's redirect target.
func (h *harness) callback(c *http.Client, aq url.Values) (string, *http.Response) {
	h.t.Helper()
	code := h.fake.IssueCode(aq.Get("nonce"), aq.Get("code_challenge"))
	resp := h.do(c, http.MethodGet, "/auth/oauth/"+h.slot+"/callback", url.Values{"code": {code}, "state": {aq.Get("state")}}, "")
	if resp.StatusCode != http.StatusFound {
		h.t.Fatalf("callback = %d, want 302", resp.StatusCode)
	}
	return resp.Header.Get("Location"), resp
}

// flow is start + callback as c.
func (h *harness) flow(c *http.Client, q url.Values) (string, *http.Response) {
	h.t.Helper()
	return h.callback(c, h.start(c, q))
}

// sessionUser is the user the response's session cookie authenticates, ""
// when it sets none.
func (h *harness) sessionUser(resp *http.Response) string {
	h.t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == authn.CookieName && c.Value != "" {
			p, err := h.tokens.Authenticate(h.ctx, c.Value)
			if err != nil {
				h.t.Fatalf("the session cookie does not authenticate: %v", err)
			}
			return p.UserID
		}
	}
	return ""
}

func (h *harness) countUsers() int {
	h.t.Helper()
	n, err := h.users.CountUsers(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return n
}

// passwordUser registers an email+password account.
func (h *harness) passwordUser(email string) model.User {
	h.t.Helper()
	u, err := h.users.RegisterLocalUser(h.ctx, "Pat", email, "password")
	if err != nil {
		h.t.Fatal(err)
	}
	return u
}

// providerUser creates a passwordless account linked to subject at the fake
// provider's issuer.
func (h *harness) providerUser(subject, email string) model.User {
	h.t.Helper()
	return h.providerUserAt(subject, email, h.fake.IssuerURL())
}

// providerUserAt creates a passwordless account linked to subject at issuer
// ("" for a row that predates the issuer column).
func (h *harness) providerUserAt(subject, email, issuer string) model.User {
	h.t.Helper()
	u, err := h.users.CreateProviderUser(h.ctx, "Ida", model.Identity{Provider: h.slot, Subject: subject, Email: email, Issuer: issuer, CreatedAt: time.Now()}, false)
	if err != nil {
		h.t.Fatal(err)
	}
	return u
}

func (h *harness) identities(userID string) []model.Identity {
	h.t.Helper()
	ids, err := h.signIn.Identities(h.ctx, userID)
	if err != nil {
		h.t.Fatal(err)
	}
	return ids
}

func TestOIDCLoginExistingIdentity(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	u := h.providerUser(h.fake.Subject, "old@example.test")

	loc, resp := h.flow(h.client(), url.Values{"return_to": {"/apps?x=1"}})
	if loc != "/apps?x=1" {
		t.Fatalf("redirect = %q, want /apps?x=1", loc)
	}
	if got := h.sessionUser(resp); got != u.ID {
		t.Fatalf("session user = %q, want %q", got, u.ID)
	}
	if n := h.countUsers(); n != 1 {
		t.Fatalf("users = %d, want 1 (no account created)", n)
	}
	// The address the provider vouches for now is recorded.
	if ids := h.identities(u.ID); len(ids) != 1 || ids[0].Email != h.fake.Email {
		t.Fatalf("identities = %+v", ids)
	}
	// The flow cookie is spent.
	for _, c := range resp.Cookies() {
		if c.Name == hoauthlogin.FlowCookie && c.MaxAge >= 0 {
			t.Fatalf("flow cookie not cleared: %+v", c)
		}
	}
}

// TestOIDCIdentityOfAnotherIssuerRefused: a subject is unique only per
// issuer. After AUTH_OIDC_ISSUER is repointed at another IdP, that IdP's user
// with the same sub must not sign in to the account the old IdP's user
// linked, nor link it to theirs.
func TestOIDCIdentityOfAnotherIssuerRefused(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	h.open = true
	victim := h.providerUserAt(h.fake.Subject, "victim@example.test", "https://old-idp.example.test")

	loc, resp := h.flow(h.client(), nil)
	if loc != "/login?error=provider_error" {
		t.Fatalf("redirect = %q, want /login?error=provider_error", loc)
	}
	if got := h.sessionUser(resp); got != "" {
		t.Fatalf("signed in as %q through another issuer", got)
	}
	if n := h.countUsers(); n != 1 {
		t.Fatalf("users = %d, want 1", n)
	}

	loc, _ = h.flow(h.signedIn(victim.ID), url.Values{"mode": {"link"}, "return_to": {"/settings"}})
	if loc != "/settings?error=identity_taken" {
		t.Fatalf("link redirect = %q", loc)
	}
	if ids := h.identities(victim.ID); len(ids) != 1 || ids[0].Issuer != "https://old-idp.example.test" {
		t.Fatalf("identities = %+v", ids)
	}
}

func TestProviderLoginNewUserRegistrationOpen(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	h.passwordUser("someone@example.test")
	h.open = true

	loc, resp := h.flow(h.client(), nil)
	if loc != "/" {
		t.Fatalf("redirect = %q, want /", loc)
	}
	userID := h.sessionUser(resp)
	if userID == "" {
		t.Fatal("no session for the new account")
	}
	if n := h.countUsers(); n != 2 {
		t.Fatalf("users = %d, want 2", n)
	}
	ids := h.identities(userID)
	if len(ids) != 1 || ids[0].Subject != h.fake.Subject || ids[0].Email != h.fake.Email || ids[0].Issuer != h.fake.IssuerURL() {
		t.Fatalf("identities = %+v", ids)
	}
	if _, err := h.users.PrimaryOrganizationID(h.ctx, userID); err != nil {
		t.Fatalf("the new account has no organization: %v", err)
	}
}

func TestProviderLoginUnknownEmailRegistrationClosed(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	h.passwordUser("someone@example.test")

	loc, resp := h.flow(h.client(), nil)
	if loc != "/login?error=registration_closed" {
		t.Fatalf("redirect = %q", loc)
	}
	if got := h.sessionUser(resp); got != "" {
		t.Fatalf("a session was started for %q", got)
	}
	if n := h.countUsers(); n != 1 {
		t.Fatalf("users = %d, want 1 (no account created)", n)
	}
}

// TestProviderLoginFreshInstanceFirstOnly: the claim step's opening holds
// only while there are no users; a racing first sign-in that finds one is
// refused like a closed registration.
func TestProviderLoginFreshInstanceFirstOnly(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	h.passwordUser("first@example.test")
	h.open, h.firstOnly = true, true
	if loc, _ := h.flow(h.client(), nil); loc != "/login?error=registration_closed" {
		t.Fatalf("redirect = %q", loc)
	}
	if n := h.countUsers(); n != 1 {
		t.Fatalf("users = %d, want 1", n)
	}
}

// TestProviderLoginEmailOfExistingAccount: an unknown provider account
// whose address an account already has is not linked to it, nor given a
// second account.
func TestProviderLoginEmailOfExistingAccount(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	owner := h.passwordUser(h.fake.Email)
	h.open = true
	loc, resp := h.flow(h.client(), nil)
	if loc != "/login?error=account_exists" {
		t.Fatalf("redirect = %q", loc)
	}
	if h.sessionUser(resp) != "" || h.countUsers() != 1 || len(h.identities(owner.ID)) != 0 {
		t.Fatal("the provider account was attached or given an account")
	}
}

func TestProviderLoginUnverifiedEmail(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	h.open = true
	h.fake.EmailVerified = false
	if loc, _ := h.flow(h.client(), nil); loc != "/login?error=email_unverified" {
		t.Fatalf("redirect = %q", loc)
	}
	if n := h.countUsers(); n != 0 {
		t.Fatalf("users = %d, want 0", n)
	}
}

func TestCallbackBadState(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	u := h.providerUser(h.fake.Subject, h.fake.Email)

	t.Run("unknown state", func(t *testing.T) {
		resp := h.do(h.client(), http.MethodGet, "/auth/oauth/oidc/callback", url.Values{"code": {"c"}, "state": {"forged"}}, "")
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/login?error=invalid_state" {
			t.Fatalf("callback = %d %q", resp.StatusCode, resp.Header.Get("Location"))
		}
		if h.sessionUser(resp) != "" {
			t.Fatal("a forged state started a session")
		}
	})
	t.Run("another browser's state", func(t *testing.T) {
		// Login CSRF: the attacker starts a flow and hands the callback
		// URL to a victim, whose browser lacks the flow cookie.
		aq := h.start(h.client(), nil)
		loc, resp := h.callback(h.client(), aq)
		if loc != "/login?error=invalid_state" || h.sessionUser(resp) != "" {
			t.Fatalf("a callback from another browser redirected to %q", loc)
		}
	})
	t.Run("replayed state", func(t *testing.T) {
		c := h.client()
		aq := h.start(c, nil)
		if _, resp := h.callback(c, aq); h.sessionUser(resp) != u.ID {
			t.Fatal("the first callback did not sign in")
		}
		if loc, resp := h.callback(c, aq); loc != "/login?error=invalid_state" || h.sessionUser(resp) != "" {
			t.Fatalf("a replayed state redirected to %q", loc)
		}
	})
	t.Run("provider error", func(t *testing.T) {
		c := h.client()
		aq := h.start(c, nil)
		resp := h.do(c, http.MethodGet, "/auth/oauth/oidc/callback", url.Values{"error": {"access_denied"}, "state": {aq.Get("state")}}, "")
		if resp.Header.Get("Location") != "/login?error=denied" {
			t.Fatalf("a denied consent redirected to %q", resp.Header.Get("Location"))
		}
	})
	t.Run("missing state", func(t *testing.T) {
		if resp := h.do(h.client(), http.MethodGet, "/auth/oauth/oidc/callback", url.Values{"code": {"c"}}, ""); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("callback without state = %d, want 400", resp.StatusCode)
		}
	})
}

func TestReturnToMustBeRelative(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	h.providerUser(h.fake.Subject, h.fake.Email)
	for _, rt := range []string{"https://evil.example/x", "//evil.example/x", "/\\evil.example", "javascript:alert(1)", "settings"} {
		if loc, _ := h.flow(h.client(), url.Values{"return_to": {rt}}); loc != "/" {
			t.Errorf("return_to %q redirected to %q, want /", rt, loc)
		}
	}
}

func TestLinkIdentity(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	u := h.passwordUser("pat@example.test")
	c := h.signedIn(u.ID)

	loc, _ := h.flow(c, url.Values{"mode": {"link"}, "return_to": {"/settings/accounts"}})
	if loc != "/settings/accounts" {
		t.Fatalf("redirect = %q", loc)
	}
	ids := h.identities(u.ID)
	if len(ids) != 1 || ids[0].Provider != model.ProviderOIDC || ids[0].Subject != h.fake.Subject {
		t.Fatalf("identities = %+v", ids)
	}

	t.Run("start needs a session", func(t *testing.T) {
		resp := h.do(h.client(), http.MethodGet, "/auth/oauth/oidc/start", url.Values{"mode": {"link"}}, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("link start without a session = %d, want 401", resp.StatusCode)
		}
	})
	t.Run("callback must carry the starter's session", func(t *testing.T) {
		other := h.passwordUser("other@example.test")
		c := h.signedIn(other.ID)
		aq := h.start(c, url.Values{"mode": {"link"}, "return_to": {"/settings"}})
		// The victim's browser: same flow cookie, someone else's session.
		u2, _ := url.Parse(h.srv.URL)
		raw, _ := h.tokens.IssueSession(h.ctx, u.ID, "test")
		c.Jar.SetCookies(u2, []*http.Cookie{{Name: authn.CookieName, Value: raw, Path: "/"}})
		if loc, _ := h.callback(c, aq); loc != "/settings?error=session_mismatch" {
			t.Fatalf("redirect = %q", loc)
		}
		if len(h.identities(other.ID)) != 0 {
			t.Fatal("linked without the starter's session")
		}
	})
}

func TestLinkIdentityTakenByOther(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	owner := h.providerUser(h.fake.Subject, h.fake.Email)
	u := h.passwordUser("pat@example.test")

	loc, _ := h.flow(h.signedIn(u.ID), url.Values{"mode": {"link"}, "return_to": {"/settings"}})
	if loc != "/settings?error=identity_taken" {
		t.Fatalf("redirect = %q", loc)
	}
	if len(h.identities(u.ID)) != 0 || len(h.identities(owner.ID)) != 1 {
		t.Fatal("the identity moved")
	}
}

func TestLinkSecondAccountOfProvider(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	u := h.providerUser("another-subject", "pat@example.test")
	loc, _ := h.flow(h.signedIn(u.ID), url.Values{"mode": {"link"}, "return_to": {"/settings"}})
	if loc != "/settings?error=provider_already_linked" {
		t.Fatalf("redirect = %q", loc)
	}
}

func TestUnlinkLastCredentialRefused(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	u := h.providerUser(h.fake.Subject, h.fake.Email)
	c := h.signedIn(u.ID)

	resp := h.do(c, http.MethodPost, "/api/v1/user/unlink-identity", nil, `{"provider":"oidc"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("unlink of the only sign-in method = %d, want 409", resp.StatusCode)
	}
	if len(h.identities(u.ID)) != 1 {
		t.Fatal("the identity was removed")
	}
}

func TestUnlinkWithPassword(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	u := h.passwordUser("pat@example.test")
	c := h.signedIn(u.ID)
	h.flow(c, url.Values{"mode": {"link"}})

	var list struct {
		Data []hoauthlogin.IdentityItem `json:"data"`
	}
	resp := h.do(c, http.MethodGet, "/api/v1/user/get-identities", nil, "")
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil || len(list.Data) != 1 || list.Data[0].Provider != "oidc" ||
		list.Data[0].Email != h.fake.Email {
		t.Fatalf("get-identities = %+v (%v)", list, err)
	}
	if resp := h.do(c, http.MethodPost, "/api/v1/user/unlink-identity", nil, `{"provider":"oidc"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("unlink = %d, want 200", resp.StatusCode)
	}
	if len(h.identities(u.ID)) != 0 {
		t.Fatal("the identity is still linked")
	}
	if resp := h.do(c, http.MethodPost, "/api/v1/user/unlink-identity", nil, `{"provider":"oidc"}`); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second unlink = %d, want 404", resp.StatusCode)
	}
	if resp := h.do(c, http.MethodPost, "/api/v1/user/unlink-identity", nil, `{"provider":"local"}`); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unlink of the password account's own row = %d, want 404", resp.StatusCode)
	}
}

// TestLegacyGoogleAccountAdopted: a Google account linked by the previous
// auth library (keyed by the SHA-1 of the subject) signs in to the same
// user, and is re-keyed by the subject.
func TestLegacyGoogleAccountAdopted(t *testing.T) {
	h := newHarness(t, model.ProviderGoogle)
	sum := sha1.Sum([]byte(h.fake.Subject)) //nolint:gosec // see the import
	legacy := hex.EncodeToString(sum[:])
	u := h.providerUserAt(legacy, "", "")

	_, resp := h.flow(h.client(), nil)
	if got := h.sessionUser(resp); got != u.ID {
		t.Fatalf("session user = %q, want %q", got, u.ID)
	}
	ids := h.identities(u.ID)
	if len(ids) != 1 || ids[0].Subject != h.fake.Subject || ids[0].Email != h.fake.Email {
		t.Fatalf("identities = %+v", ids)
	}
}

func TestStartRefusals(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	for _, tc := range []struct {
		path string
		q    url.Values
		want int
	}{
		{"/auth/oauth/oidc/start", url.Values{"mode": {"bogus"}}, http.StatusBadRequest},
		{"/auth/oauth/google/start", nil, http.StatusNotFound},
	} {
		if resp := h.do(h.client(), http.MethodGet, tc.path, tc.q, ""); resp.StatusCode != tc.want {
			t.Errorf("%s %v = %d, want %d", tc.path, tc.q, resp.StatusCode, tc.want)
		}
	}
}

// TestFormPostCallback: Apple's form_post answer is re-sent to the GET
// callback, which then completes the sign-in in the browser that started it.
func TestFormPostCallback(t *testing.T) {
	h := newHarness(t, model.ProviderOIDC)
	h.open = true
	c := h.client()
	aq := h.start(c, nil)
	code := h.fake.IssueCode(aq.Get("nonce"), aq.Get("code_challenge"))
	form := url.Values{"code": {code}, "state": {aq.Get("state")}, "user": {`{"name":{"firstName":"Ada","lastName":"L"}}`}}
	h.fake.Name = ""
	req, _ := http.NewRequestWithContext(h.ctx, http.MethodPost, h.srv.URL+"/auth/oauth/oidc/callback", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("form_post = %d, want 303", resp.StatusCode)
	}
	next := h.do(c, http.MethodGet, resp.Header.Get("Location"), nil, "")
	userID := h.sessionUser(next)
	if next.StatusCode != http.StatusFound || userID == "" {
		t.Fatalf("the re-sent callback = %d %q", next.StatusCode, next.Header.Get("Location"))
	}
	u, err := h.users.GetUser(h.ctx, userID)
	if err != nil || u.Name != "Ada L" {
		t.Fatalf("new user = %+v (%v), want the name from Apple's user field", u, err)
	}
}
