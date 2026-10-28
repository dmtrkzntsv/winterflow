package authn_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"winterflow/internal/app/web/middleware/authn"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

// fakeAuth knows three tokens, one per kind.
type fakeAuth map[string]model.Principal

func (f fakeAuth) Authenticate(_ context.Context, raw string) (model.Principal, error) {
	p, ok := f[raw]
	if !ok {
		return model.Principal{}, model.ErrUnauthorized
	}
	return p, nil
}

var tokens = fakeAuth{
	"wfs_session": {UserID: "u-session", TokenID: "t1", Kind: model.TokenSession, Scope: model.ScopeFull},
	"wfp_pat":     {UserID: "u-pat", TokenID: "t2", Kind: model.TokenPAT, Scope: model.ScopeFull},
	"wfo_oauth":   {UserID: "u-oauth", TokenID: "t3", Kind: model.TokenOAuth, Scope: model.ScopeMCP},
}

// serve runs r through the middleware in front of a handler that answers
// 200 with the authenticated user id as its body.
func serve(t *testing.T, opts authn.Options, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	h := authn.Middleware(tokens, opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := webutil.Principal(r)
		if !ok || p.UserID == "" || p.TokenID == "" {
			t.Errorf("Principal behind the middleware = %+v, %v", p, ok)
		}
		_, _ = w.Write([]byte(p.UserID))
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func expect(t *testing.T, w *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d; body %s", w.Code, status, w.Body)
	}
	if body != "" && w.Body.String() != body {
		t.Fatalf("body = %q, want %q", w.Body, body)
	}
}

func withCookie(r *http.Request, v string) *http.Request {
	r.AddCookie(&http.Cookie{Name: authn.CookieName, Value: v})
	return r
}

func TestBearerPAT(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	r.Header.Set("Authorization", "Bearer wfp_pat")
	expect(t, serve(t, authn.Options{}, r), http.StatusOK, "u-pat")

	// The scheme is case-insensitive.
	r = httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	r.Header.Set("Authorization", "bearer wfp_pat")
	expect(t, serve(t, authn.Options{}, r), http.StatusOK, "u-pat")

	r = httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	r.Header.Set("Authorization", "Bearer wfp_unknown")
	expect(t, serve(t, authn.Options{}, r), http.StatusUnauthorized, "")
}

func TestBasicPAT(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	r.SetBasicAuth("anything", "wfp_pat")
	expect(t, serve(t, authn.Options{}, r), http.StatusOK, "u-pat")

	r = httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	r.SetBasicAuth("anything", "wrong")
	expect(t, serve(t, authn.Options{}, r), http.StatusUnauthorized, "")
}

func TestCookieSession(t *testing.T) {
	r := withCookie(httptest.NewRequest(http.MethodGet, "/api/v1/x", nil), "wfs_session")
	expect(t, serve(t, authn.Options{}, r), http.StatusOK, "u-session")

	r = withCookie(httptest.NewRequest(http.MethodGet, "/api/v1/x", nil), "wfs_revoked")
	expect(t, serve(t, authn.Options{}, r), http.StatusUnauthorized, "")

	// No credential at all.
	expect(t, serve(t, authn.Options{}, httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)), http.StatusUnauthorized, "")
}

// TestAuthorizationHeaderWins: a bad Authorization header does not fall
// back to a valid cookie.
func TestAuthorizationHeaderWins(t *testing.T) {
	r := withCookie(httptest.NewRequest(http.MethodGet, "/api/v1/x", nil), "wfs_session")
	r.Header.Set("Authorization", "Bearer wfp_unknown")
	expect(t, serve(t, authn.Options{}, r), http.StatusUnauthorized, "")

	r = withCookie(httptest.NewRequest(http.MethodGet, "/api/v1/x", nil), "wfs_session")
	r.Header.Set("Authorization", "Digest x")
	expect(t, serve(t, authn.Options{}, r), http.StatusUnauthorized, "")
}

func TestOAuthTokenRejectedOnREST(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	r.Header.Set("Authorization", "Bearer wfo_oauth")
	expect(t, serve(t, authn.Options{}, r), http.StatusUnauthorized, "")
}

func TestOAuthTokenAllowedWithOption(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer wfo_oauth")
	expect(t, serve(t, authn.Options{AllowOAuth: true}, r), http.StatusOK, "u-oauth")
}

func TestCookiePostWithoutCSRFHeader(t *testing.T) {
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r := withCookie(httptest.NewRequest(m, "/api/v1/x", nil), "wfs_session")
		expect(t, serve(t, authn.Options{}, r), http.StatusForbidden, "")
	}
	// Any value but "1" is refused too.
	r := withCookie(httptest.NewRequest(http.MethodPost, "/api/v1/x", nil), "wfs_session")
	r.Header.Set(authn.CSRFHeader, "true")
	expect(t, serve(t, authn.Options{}, r), http.StatusForbidden, "")
}

func TestCookiePostWithCSRFHeader(t *testing.T) {
	r := withCookie(httptest.NewRequest(http.MethodPost, "/api/v1/x", nil), "wfs_session")
	r.Header.Set(authn.CSRFHeader, "1")
	expect(t, serve(t, authn.Options{}, r), http.StatusOK, "u-session")
}

func TestBearerPostNoCSRFNeeded(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/x", nil)
	r.Header.Set("Authorization", "Bearer wfp_pat")
	expect(t, serve(t, authn.Options{}, r), http.StatusOK, "u-pat")

	r = httptest.NewRequest(http.MethodPost, "/api/v1/x", nil)
	r.SetBasicAuth("", "wfs_session")
	expect(t, serve(t, authn.Options{}, r), http.StatusOK, "u-session")
}

func TestUnauthorizedChallenge(t *testing.T) {
	const challenge = `Bearer resource_metadata="https://wf.example/.well-known/oauth-protected-resource"`
	w := serve(t, authn.Options{Challenge: challenge}, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	expect(t, w, http.StatusUnauthorized, "")
	if got := w.Header().Get("WWW-Authenticate"); got != challenge {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
	// Without a challenge configured, none is sent.
	w = serve(t, authn.Options{}, httptest.NewRequest(http.MethodGet, "/api/v1/x", nil))
	if got := w.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("WWW-Authenticate = %q, want none", got)
	}
}

func TestRequireAppRequest(t *testing.T) {
	h := authn.RequireAppRequest(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct {
		header string
		want   int
	}{{"", http.StatusForbidden}, {"0", http.StatusForbidden}, {"1", http.StatusNoContent}} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		if tc.header != "" {
			r.Header.Set(authn.CSRFHeader, tc.header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != tc.want {
			t.Errorf("header %q: status = %d, want %d", tc.header, rec.Code, tc.want)
		}
	}
}
