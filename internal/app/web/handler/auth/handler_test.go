package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"winterflow/internal/app/web/middleware/authn"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

type fakeReg struct {
	users        int
	bootstrapped []string
	registered   []string
}

func (f *fakeReg) CountUsers(context.Context) (int, error) { return f.users, nil }
func (f *fakeReg) BootstrapLocalAdmin(_ context.Context, _, email, _ string) (model.User, error) {
	if f.users > 0 {
		return model.User{}, model.ErrNotBootstrap
	}
	f.users = 1
	f.bootstrapped = append(f.bootstrapped, email)
	return model.User{ID: "admin-1"}, nil
}
func (f *fakeReg) RegisterLocalUser(_ context.Context, _, email, _ string) (model.User, error) {
	if email == "taken@x.io" {
		return model.User{}, model.ErrEmailTaken
	}
	f.users++
	f.registered = append(f.registered, email)
	return model.User{ID: "u-" + email}, nil
}

// VerifyLocalCredentials knows one account: alice@x.io / longenough1.
func (f *fakeReg) VerifyLocalCredentials(_ context.Context, email, password string) (model.User, error) {
	if email != "alice@x.io" || password != "longenough1" {
		return model.User{}, model.ErrInvalidCredentials
	}
	return model.User{ID: "alice", Name: "Alice", AvatarURL: "/avatar/old.png"}, nil
}

// fakeSessions is an in-memory session store that also authenticates, so a
// cookie it issued can be checked through the real middleware.
type fakeSessions struct {
	n       int
	live    map[string]model.Principal // raw -> principal
	revoked []string
}

func (f *fakeSessions) IssueSession(_ context.Context, userID, _ string) (string, error) {
	f.n++
	raw := "wfs_" + strings.Repeat("x", f.n)
	if f.live == nil {
		f.live = map[string]model.Principal{}
	}
	f.live[raw] = model.Principal{UserID: userID, TokenID: raw + "-id", Kind: model.TokenSession, Scope: model.ScopeFull}
	return raw, nil
}

func (f *fakeSessions) RevokeToken(_ context.Context, tokenID string) error {
	f.revoked = append(f.revoked, tokenID)
	for raw, p := range f.live {
		if p.TokenID == tokenID {
			delete(f.live, raw)
		}
	}
	return nil
}

func (f *fakeSessions) Authenticate(_ context.Context, raw string) (model.Principal, error) {
	p, ok := f.live[raw]
	if !ok {
		return model.Principal{}, model.ErrUnauthorized
	}
	return p, nil
}

type harness struct {
	*Handler
	reg      *fakeReg
	sessions *fakeSessions
	signedIn []string // AfterSignIn calls
}

func newHarness(t *testing.T, mode string, users int) *harness {
	t.Helper()
	hs := &harness{reg: &fakeReg{users: users}, sessions: &fakeSessions{}}
	hs.Handler = NewHandler(&Deps{
		Logger:      logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
		Cfg:         config.NewServerConfig(mode),
		Users:       hs.reg,
		Sessions:    hs.sessions,
		AfterSignIn: func(_ context.Context, userID string) { hs.signedIn = append(hs.signedIn, userID) },
	})
	return hs
}

func newHandler(t *testing.T, mode string, users int) (*Handler, *fakeReg) {
	t.Helper()
	hs := newHarness(t, mode, users)
	return hs.Handler, hs.reg
}

// sessionCookie returns the wf_session cookie w sets, or nil.
func sessionCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == authn.CookieName {
			return c
		}
	}
	return nil
}

func login(h *Handler, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Login(w, r)
	return w
}

func register(h *Handler, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/v1/auth/register", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Register(w, r)
	return w
}

const valid = `{"name":"Alice","email":"Alice@X.io","password":"longenough1"}`

func TestRegisterStandaloneClaimStep(t *testing.T) {
	h, f := newHandler(t, "standalone", 0)
	w := register(h, valid)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if len(f.bootstrapped) != 1 || f.bootstrapped[0] != "alice@x.io" {
		t.Errorf("bootstrapped = %v (want normalized email via claim path)", f.bootstrapped)
	}
	// Second registration in standalone: closed.
	if w := register(h, `{"name":"Bob","email":"b@x.io","password":"longenough1"}`); w.Code != http.StatusBadRequest {
		t.Errorf("standalone second register: code = %d, want 400", w.Code)
	}
}

func TestRegisterDistributedCreatesOwnOrg(t *testing.T) {
	h, f := newHandler(t, "distributed", 5)
	if w := register(h, valid); w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	if len(f.registered) != 1 {
		t.Errorf("registered = %v", f.registered)
	}
}

func TestRegisterDistributedToggleOff(t *testing.T) {
	t.Setenv("REGISTRATION_ENABLED", "false")
	// Non-empty instance: closed.
	h, _ := newHandler(t, "distributed", 5)
	if w := register(h, valid); w.Code != http.StatusBadRequest {
		t.Errorf("toggle off: code = %d, want 400", w.Code)
	}
	// Fresh instance: the claim step ignores the toggle (never brick).
	h2, f2 := newHandler(t, "distributed", 0)
	if w := register(h2, valid); w.Code != http.StatusOK {
		t.Errorf("toggle off + zero users: code = %d, want 200", w.Code)
	}
	if len(f2.bootstrapped)+len(f2.registered) != 1 {
		t.Error("first user not created")
	}
}

func TestRegisterValidation(t *testing.T) {
	h, _ := newHandler(t, "standalone", 0)
	for _, body := range []string{
		`{}`,
		`{"name":"A","email":"bad","password":"longenough1"}`,
		`{"name":"A","email":"a@b.io","password":"abc"}`,
		`{"name":"","email":"a@b.io","password":"longenough1"}`,
	} {
		if w := register(h, body); w.Code != http.StatusBadRequest {
			t.Errorf("body %s: code = %d, want 400", body, w.Code)
		}
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	h, _ := newHandler(t, "distributed", 3)
	if w := register(h, `{"name":"E","email":"taken@x.io","password":"longenough1"}`); w.Code != http.StatusBadRequest {
		t.Errorf("dup email: code = %d, want 400", w.Code)
	}
}

func TestState(t *testing.T) {
	h, _ := newHandler(t, "standalone", 0)
	w := httptest.NewRecorder()
	h.State(w, httptest.NewRequest("GET", "/x", nil))
	var resp struct {
		Data struct {
			Bootstrap           bool `json:"bootstrap"`
			RegistrationEnabled bool `json:"registration_enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Data.Bootstrap || !resp.Data.RegistrationEnabled {
		t.Errorf("fresh standalone state = %+v", resp.Data)
	}

	// Standalone with users: registration closed.
	h2, _ := newHandler(t, "standalone", 1)
	w2 := httptest.NewRecorder()
	h2.State(w2, httptest.NewRequest("GET", "/x", nil))
	_ = json.Unmarshal(w2.Body.Bytes(), &resp)
	if resp.Data.Bootstrap || resp.Data.RegistrationEnabled {
		t.Errorf("claimed standalone state = %+v", resp.Data)
	}
}

func TestRegisterIssuesSession(t *testing.T) {
	hs := newHarness(t, "standalone", 0)
	w := register(hs.Handler, valid)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}
	c := sessionCookie(w)
	if c == nil || c.Value == "" {
		t.Fatal("register must set the session cookie")
	}
	if p, ok := hs.sessions.live[c.Value]; !ok || p.UserID != "admin-1" {
		t.Fatalf("cookie session = %+v, %v", p, ok)
	}
	// Standalone: the registration auto-claims the embedded server.
	if len(hs.signedIn) != 1 || hs.signedIn[0] != "admin-1" {
		t.Fatalf("AfterSignIn calls = %v", hs.signedIn)
	}
	// A refused registration starts no session.
	w = register(hs.Handler, `{"name":"Bob","email":"b@x.io","password":"longenough1"}`)
	if w.Code != http.StatusBadRequest || sessionCookie(w) != nil {
		t.Fatalf("closed register: code %d, cookie %v", w.Code, sessionCookie(w))
	}
}

func TestLoginSetsCookie(t *testing.T) {
	hs := newHarness(t, "distributed", 1)
	t.Setenv("WEB_URL", "https://wf.example")
	w := login(hs.Handler, `{"email":" Alice@X.io ","password":"longenough1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}
	c := sessionCookie(w)
	if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge <= 0 {
		t.Fatalf("cookie = %+v", c)
	}
	if p := hs.sessions.live[c.Value]; p.UserID != "alice" {
		t.Fatalf("session user = %q", p.UserID)
	}
	var resp struct {
		Data LoginResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// The stale relative avatar of the old auth library is not passed on.
	want := SessionUser{ID: "alice", Name: "Alice", Email: "alice@x.io"}
	if resp.Data.User != want {
		t.Fatalf("user = %+v, want %+v", resp.Data.User, want)
	}
	// Distributed: AfterSignIn is wired by the caller per topology; the
	// handler calls it whenever set.
	if len(hs.signedIn) != 1 {
		t.Fatalf("AfterSignIn calls = %v", hs.signedIn)
	}
}

func TestLoginBadPassword(t *testing.T) {
	hs := newHarness(t, "standalone", 1)
	wrong := login(hs.Handler, `{"email":"alice@x.io","password":"nope"}`)
	unknown := login(hs.Handler, `{"email":"bob@x.io","password":"longenough1"}`)
	empty := login(hs.Handler, `{"email":"","password":""}`)
	for name, w := range map[string]*httptest.ResponseRecorder{"wrong": wrong, "unknown": unknown, "empty": empty} {
		if w.Code != http.StatusUnauthorized || sessionCookie(w) != nil {
			t.Errorf("%s: code %d, cookie %v", name, w.Code, sessionCookie(w))
		}
	}
	// Same answer for an unknown email as for a wrong password.
	if wrong.Body.String() != unknown.Body.String() {
		t.Errorf("unknown email answer %s differs from wrong password %s", unknown.Body, wrong.Body)
	}
	if len(hs.sessions.live) != 0 || len(hs.signedIn) != 0 {
		t.Errorf("failed logins started sessions: %v, %v", hs.sessions.live, hs.signedIn)
	}
}

func TestStandaloneLoginAutoClaims(t *testing.T) {
	hs := newHarness(t, "standalone", 1)
	if w := login(hs.Handler, `{"email":"alice@x.io","password":"longenough1"}`); w.Code != http.StatusOK {
		t.Fatalf("code=%d", w.Code)
	}
	if w := login(hs.Handler, `{"email":"alice@x.io","password":"longenough1"}`); w.Code != http.StatusOK {
		t.Fatalf("code=%d", w.Code)
	}
	// Every successful login runs it (it is a no-op once claimed).
	if len(hs.signedIn) != 2 || hs.signedIn[0] != "alice" {
		t.Fatalf("AfterSignIn calls = %v", hs.signedIn)
	}
}

func TestLogoutRevokes(t *testing.T) {
	hs := newHarness(t, "standalone", 1)
	c := sessionCookie(login(hs.Handler, `{"email":"alice@x.io","password":"longenough1"}`))
	if c == nil {
		t.Fatal("no session cookie")
	}
	// Logout runs behind the real middleware, as routed.
	logout := authn.Middleware(hs.sessions, authn.Options{})(http.HandlerFunc(hs.Logout))
	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
		r.AddCookie(c)
		r.Header.Set(authn.CSRFHeader, "1")
		w := httptest.NewRecorder()
		logout.ServeHTTP(w, r)
		return w
	}
	w := call()
	if w.Code != http.StatusOK {
		t.Fatalf("logout code=%d body=%s", w.Code, w.Body)
	}
	if cleared := sessionCookie(w); cleared == nil || cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Fatalf("logout cookie = %+v, want cleared", cleared)
	}
	if len(hs.sessions.revoked) != 1 {
		t.Fatalf("revoked = %v", hs.sessions.revoked)
	}
	// The cookie no longer authenticates.
	if w := call(); w.Code != http.StatusUnauthorized {
		t.Fatalf("after logout: code=%d, want 401", w.Code)
	}
}

func TestLogoutWithPATRevokesNothing(t *testing.T) {
	hs := newHarness(t, "standalone", 1)
	r := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	r = webutil.WithPrincipal(r, model.Principal{UserID: "alice", TokenID: "pat-1", Kind: model.TokenPAT})
	w := httptest.NewRecorder()
	hs.Logout(w, r)
	if w.Code != http.StatusOK || len(hs.sessions.revoked) != 0 {
		t.Fatalf("code %d, revoked %v", w.Code, hs.sessions.revoked)
	}
}

func TestStateListsNoProvidersYet(t *testing.T) {
	h, _ := newHandler(t, "standalone", 1)
	w := httptest.NewRecorder()
	h.State(w, httptest.NewRequest("GET", "/x", nil))
	var resp struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if string(resp.Data["providers"]) != "[]" || string(resp.Data["oauth_server"]) != "false" {
		t.Fatalf("state = %s", w.Body)
	}
}
