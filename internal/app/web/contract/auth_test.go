package contract_test

import (
	"net/http"
	"net/url"
	"testing"

	"winterflow/internal/app/web/middleware/authn"
)

func TestAuthClaimFlow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	anon := e.anon()

	var st struct {
		Bootstrap           bool       `json:"bootstrap"`
		RegistrationEnabled bool       `json:"registration_enabled"`
		Providers           []struct{} `json:"providers"`
		OAuthServer         bool       `json:"oauth_server"`
	}
	anon.get(t, "/api/v1/auth/state", nil).expect(t, http.StatusOK).data(t, &st)
	if !st.Bootstrap || !st.RegistrationEnabled || st.Providers == nil || len(st.Providers) != 0 || st.OAuthServer {
		t.Fatalf("fresh instance state = %+v, want bootstrap + open, no providers", st)
	}

	t.Run("register rejects a short password", func(t *testing.T) {
		anon.post(t, "/api/v1/auth/register", map[string]string{
			"name": "X", "email": "x@example.test", "password": "1",
		}).expect(t, http.StatusBadRequest)
	})

	owner := e.claim(t)

	anon.get(t, "/api/v1/auth/state", nil).expect(t, http.StatusOK).data(t, &st)
	if st.Bootstrap || st.RegistrationEnabled {
		t.Fatalf("claimed standalone state = %+v, want closed", st)
	}
	t.Run("standalone registration closes after the claim", func(t *testing.T) {
		env := anon.post(t, "/api/v1/auth/register", map[string]string{
			"name": "Late", "email": "late@example.test", "password": "password",
		}).expect(t, http.StatusBadRequest).data(t, nil)
		if env.Message != "registration is disabled" {
			t.Fatalf("message = %q", env.Message)
		}
	})
	t.Run("a wrong password and an unknown email get the same 401", func(t *testing.T) {
		wrong := e.anon().tryLogin(t, owner.email, "wrong").expect(t, http.StatusUnauthorized)
		unknown := e.anon().tryLogin(t, "nobody@example.test", ownerPassword).expect(t, http.StatusUnauthorized)
		if string(wrong.Body) != string(unknown.Body) {
			t.Fatalf("wrong password %s, unknown email %s", wrong.Body, unknown.Body)
		}
		if sessionCookie(wrong) != nil || sessionCookie(unknown) != nil {
			t.Fatal("a failed login set the session cookie")
		}
	})
	t.Run("login answers the user and sets the cookie", func(t *testing.T) {
		var d struct {
			User struct {
				ID    string `json:"id"`
				Email string `json:"email"`
			} `json:"user"`
		}
		r := e.anon().tryLogin(t, owner.email, ownerPassword).expect(t, http.StatusOK)
		r.data(t, &d)
		if d.User.ID != owner.userID || d.User.Email != owner.email {
			t.Fatalf("login user = %+v", d.User)
		}
		c := sessionCookie(r)
		if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
			t.Fatalf("session cookie = %+v", c)
		}
	})
	t.Run("logout revokes the session", func(t *testing.T) {
		s := e.anon()
		s.login(t, owner.email, ownerPassword)
		s.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusOK)
		r := s.post(t, "/api/v1/auth/logout", nil).expect(t, http.StatusOK)
		if c := sessionCookie(r); c == nil || c.MaxAge >= 0 {
			t.Fatalf("logout cookie = %+v, want cleared", c)
		}
		s.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusUnauthorized)
		// The owner's other session is untouched.
		owner.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusOK)
	})
}

// sessionCookie returns the wf_session cookie a response sets, or nil.
func sessionCookie(r *response) *http.Cookie {
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if c.Name == authn.CookieName {
			return c
		}
	}
	return nil
}

// sessionToken returns the session cookie c's jar holds.
func (c *client) sessionToken(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(c.e.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range c.http.Jar.Cookies(u) {
		if k.Name == authn.CookieName {
			return k.Value
		}
	}
	t.Fatal("no session cookie in the jar")
	return ""
}

// TestRegisterIssuesSession: a registration signs the new account in.
func TestRegisterIssuesSession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.newClient()
	c.post(t, "/api/v1/auth/register", map[string]string{
		"name": "Owner", "email": "owner@example.test", "password": ownerPassword,
	}).expect(t, http.StatusOK)
	c.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusOK)
	// Standalone: the registration already claimed the embedded server.
	if id := c.serverID(t); id == "" {
		t.Fatal("no server after registering")
	}
}

// TestLogoutAll ends every session of the caller, the presenting one
// included, and leaves personal access tokens working.
func TestLogoutAll(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	other := e.anon()
	other.login(t, owner.email, ownerPassword)
	bearer := owner.pat(t)
	// The caller's own cookie, kept aside: the jar drops it on the clear.
	own := owner.sessionToken(t)

	r := owner.post(t, "/api/v1/user/logout-all", nil).expect(t, http.StatusOK)
	if c := sessionCookie(r); c == nil || c.MaxAge >= 0 {
		t.Fatalf("logout-all cookie = %+v, want cleared", c)
	}
	other.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusUnauthorized)
	// The presenting session itself is revoked, not just forgotten.
	e.anon().do(t, call{Method: http.MethodGet, Path: "/api/v1/user/get-profile",
		Header: http.Header{"Cookie": {authn.CookieName + "=" + own}}}).expect(t, http.StatusUnauthorized)
	bearer.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusOK)
}

// TestCookieWritesNeedCSRFHeader: a cookie-authenticated POST without
// X-Winterflow-Request (a cross-site form) is refused; a bearer one is not.
func TestCookieWritesNeedCSRFHeader(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	noCSRF := http.Header{authn.CSRFHeader: nil}
	owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/user/create-token",
		Body: map[string]any{"name": "x"}, Header: noCSRF}).expect(t, http.StatusForbidden)
	// Reads need no header.
	owner.do(t, call{Method: http.MethodGet, Path: "/api/v1/user/get-tokens", Header: noCSRF}).expect(t, http.StatusOK)
	bearer := owner.pat(t)
	bearer.do(t, call{Method: http.MethodPost, Path: "/api/v1/user/create-token",
		Body: map[string]any{"name": "y"}, Header: noCSRF}).expect(t, http.StatusOK)
}
