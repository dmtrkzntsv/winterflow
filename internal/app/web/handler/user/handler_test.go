package user

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"winterflow/internal/app/web/middleware/authn"
	webutil "winterflow/internal/app/web/util"

	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

type fakeTokens struct {
	created         []model.AccessToken
	deleted         []string
	deleteBy        string
	sessionsRevoked []string
}

func (f *fakeTokens) RevokeSessions(_ context.Context, userID string) error {
	f.sessionsRevoked = append(f.sessionsRevoked, userID)
	return nil
}

func (f *fakeTokens) CreatePAT(_ context.Context, userID, name string, expiresAt *time.Time) (model.AccessToken, string, error) {
	rec := model.AccessToken{ID: "tok-1", UserID: userID, Kind: model.TokenPAT, Name: name, Prefix: "wfp_abcd1234", ExpiresAt: expiresAt, CreatedAt: time.Now()}
	f.created = append(f.created, rec)
	return rec, "wfp_abcd1234SECRETSECRETSECRETSECRETSECRET", nil
}
func (f *fakeTokens) ListPATs(_ context.Context, userID string) ([]model.AccessToken, error) {
	return f.created, nil
}
func (f *fakeTokens) DeletePAT(_ context.Context, userID, tokenID string) error {
	f.deleteBy = userID
	if tokenID != "tok-1" {
		return model.ErrTokenNotFound
	}
	f.deleted = append(f.deleted, tokenID)
	return nil
}

func authed(r *http.Request) *http.Request {
	return webutil.WithPrincipal(r, model.Principal{UserID: "user-1", Kind: model.TokenSession})
}

func newHandler() (*Handler, *fakeTokens) {
	f := &fakeTokens{}
	return NewHandler(&Deps{
		Logger: logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
		Tokens: f,
	}), f
}

func TestCreateTokenReturnsPlaintextOnce(t *testing.T) {
	h, _ := newHandler()
	r := authed(httptest.NewRequest("POST", "/api/v1/user/create-token",
		strings.NewReader(`{"name":"ci","expires_in_days":30}`)))
	w := httptest.NewRecorder()
	h.CreateToken(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Token     string  `json:"token"`
			Prefix    string  `json:"prefix"`
			ExpiresAt *string `json:"expires_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.Data.Token, "wfp_") {
		t.Errorf("token = %q", resp.Data.Token)
	}
	if resp.Data.ExpiresAt == nil {
		t.Error("expires_in_days was sent but expires_at is null")
	}
}

func TestCreateTokenRequiresName(t *testing.T) {
	h, _ := newHandler()
	for _, body := range []string{`{}`, `{"name":""}`, `{"name":"` + strings.Repeat("x", 65) + `"}`} {
		r := authed(httptest.NewRequest("POST", "/x", strings.NewReader(body)))
		w := httptest.NewRecorder()
		h.CreateToken(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %s: code = %d, want 400", body, w.Code)
		}
	}
}

func TestGetTokensListsWithoutPlaintext(t *testing.T) {
	h, f := newHandler()
	_, _, _ = f.CreatePAT(context.Background(), "user-1", "ci", nil)
	used := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	f.created = append(f.created, model.AccessToken{ID: "tok-2", Kind: model.TokenPAT, Name: "used", LastUsedAt: used})
	r := authed(httptest.NewRequest("GET", "/x", nil))
	w := httptest.NewRecorder()
	h.GetTokens(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "SECRET") {
		t.Error("list response leaks plaintext")
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 || resp.Data[0]["token_id"] != "tok-1" || resp.Data[0]["prefix"] != "wfp_abcd1234" {
		t.Fatalf("data = %v", resp.Data)
	}
	if resp.Data[0]["last_used_at"] != nil {
		t.Errorf("never-used token: last_used_at = %v, want null", resp.Data[0]["last_used_at"])
	}
	if resp.Data[1]["last_used_at"] != "2026-10-01T12:00:00Z" {
		t.Errorf("used token: last_used_at = %v", resp.Data[1]["last_used_at"])
	}
}

func TestDeleteTokenScopesAndErrors(t *testing.T) {
	h, f := newHandler()
	r := authed(httptest.NewRequest("POST", "/x", strings.NewReader(`{"token_id":"tok-1"}`)))
	w := httptest.NewRecorder()
	h.DeleteToken(w, r)
	if w.Code != http.StatusOK || f.deleteBy != "user-1" {
		t.Fatalf("code=%d deleteBy=%q", w.Code, f.deleteBy)
	}

	r = authed(httptest.NewRequest("POST", "/x", strings.NewReader(`{"token_id":"other"}`)))
	w = httptest.NewRecorder()
	h.DeleteToken(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing token: code = %d, want 400", w.Code)
	}
}

func TestUnauthenticatedIs401(t *testing.T) {
	h, _ := newHandler()
	w := httptest.NewRecorder()
	h.GetTokens(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("code = %d, want 401", w.Code)
	}
}

type fakeProfileStore struct {
	creds      *model.Credentials
	verifyErr  error
	setCalls   []bool // mustChange flag per SetPassword call
	setUserIDs []string
	addErr     error
	added      []string // "<user>:<email>:<password>" per AddPassword call
}

func (f *fakeProfileStore) AddPassword(_ context.Context, userID, email, pw string) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.added = append(f.added, userID+":"+email+":"+pw)
	return nil
}

func (f *fakeProfileStore) GetUser(_ context.Context, userID string) (model.User, error) {
	return model.User{ID: userID, Name: "Alice"}, nil
}
func (f *fakeProfileStore) GetCredentials(context.Context, string) (model.Credentials, error) {
	if f.creds == nil {
		return model.Credentials{}, model.ErrorUserNotFound
	}
	return *f.creds, nil
}
func (f *fakeProfileStore) VerifyLocalCredentials(context.Context, string, string) (model.User, error) {
	if f.verifyErr != nil {
		return model.User{}, f.verifyErr
	}
	return model.User{ID: "user-1"}, nil
}
func (f *fakeProfileStore) SetPassword(_ context.Context, userID, _ string, mustChange bool) error {
	f.setUserIDs = append(f.setUserIDs, userID)
	f.setCalls = append(f.setCalls, mustChange)
	return nil
}

func newProfileHandler(f *fakeProfileStore) *Handler {
	return NewHandler(&Deps{
		Logger: logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
		Tokens: &fakeTokens{},
		Users:  f,
	})
}

func TestGetProfileShape(t *testing.T) {
	f := &fakeProfileStore{creds: &model.Credentials{Email: "a@b.io", MustChangePassword: true}}
	h := newProfileHandler(f)
	r := authed(httptest.NewRequest("GET", "/x", nil))
	w := httptest.NewRecorder()
	h.GetProfile(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	var resp struct {
		Data struct {
			UserID             string `json:"user_id"`
			Email              string `json:"email"`
			MustChangePassword bool   `json:"must_change_password"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.UserID != "user-1" || resp.Data.Email != "a@b.io" || !resp.Data.MustChangePassword {
		t.Errorf("profile = %+v", resp.Data)
	}
	if strings.Contains(w.Body.String(), `"role"`) {
		t.Errorf("profile still carries a role: %s", w.Body.String())
	}
}

func TestGetProfileGoogleOnlyHasNoEmail(t *testing.T) {
	h := newProfileHandler(&fakeProfileStore{creds: nil})
	r := authed(httptest.NewRequest("GET", "/x", nil))
	w := httptest.NewRecorder()
	h.GetProfile(w, r)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "must_change_password\":true") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestChangePassword(t *testing.T) {
	f := &fakeProfileStore{creds: &model.Credentials{Email: "a@b.io", MustChangePassword: true}}
	h := newProfileHandler(f)

	// Success clears must-change.
	r := authed(httptest.NewRequest("POST", "/x", strings.NewReader(`{"current_password":"old-temp","new_password":"brand-new-pass1"}`)))
	w := httptest.NewRecorder()
	h.ChangePassword(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if len(f.setCalls) != 1 || f.setCalls[0] != false {
		t.Errorf("SetPassword calls = %v, want one with mustChange=false", f.setCalls)
	}

	// Wrong current password → 400.
	f.verifyErr = model.ErrInvalidCredentials
	r = authed(httptest.NewRequest("POST", "/x", strings.NewReader(`{"current_password":"nope","new_password":"brand-new-pass1"}`)))
	w = httptest.NewRecorder()
	h.ChangePassword(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("wrong current: code = %d, want 400", w.Code)
	}

	// Too-short new password → 400.
	f.verifyErr = nil
	r = authed(httptest.NewRequest("POST", "/x", strings.NewReader(`{"current_password":"old","new_password":"abc"}`)))
	w = httptest.NewRecorder()
	h.ChangePassword(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("short new: code = %d, want 400", w.Code)
	}
}

func TestChangePasswordGoogleOnly400(t *testing.T) {
	h := newProfileHandler(&fakeProfileStore{creds: nil})
	r := authed(httptest.NewRequest("POST", "/x", strings.NewReader(`{"current_password":"x","new_password":"long-enough-pw1"}`)))
	w := httptest.NewRecorder()
	h.ChangePassword(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", w.Code)
	}
}

func TestSetPassword(t *testing.T) {
	post := func(f *fakeProfileStore, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		newProfileHandler(f).SetPassword(w, authed(httptest.NewRequest("POST", "/x", strings.NewReader(body))))
		return w
	}
	f := &fakeProfileStore{}
	if w := post(f, `{"email":"a@b.io","new_password":"long-enough"}`); w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if len(f.added) != 1 || f.added[0] != "user-1:a@b.io:long-enough" {
		t.Fatalf("AddPassword calls = %v", f.added)
	}
	for _, c := range []struct {
		name, body string
		err        error
		want       int
	}{
		{"too short", `{"email":"a@b.io","new_password":"abc"}`, nil, http.StatusBadRequest},
		{"no email", `{"email":" ","new_password":"long-enough"}`, nil, http.StatusBadRequest},
		{"has a password", `{"email":"a@b.io","new_password":"long-enough"}`, model.ErrPasswordAlreadySet, http.StatusConflict},
		{"email of another account", `{"email":"a@b.io","new_password":"long-enough"}`, model.ErrEmailTaken, http.StatusConflict},
		{"not an identity's email", `{"email":"x@b.io","new_password":"long-enough"}`, model.ErrNotIdentityEmail, http.StatusBadRequest},
		{"database", `{"email":"a@b.io","new_password":"long-enough"}`, errors.New("disk I/O error"), http.StatusInternalServerError},
	} {
		f := &fakeProfileStore{addErr: c.err}
		if w := post(f, c.body); w.Code != c.want {
			t.Errorf("%s: code = %d, want %d (%s)", c.name, w.Code, c.want, w.Body.String())
		}
		if len(f.added) != 0 {
			t.Errorf("%s: a password was set", c.name)
		}
	}
}

func TestLogoutAll(t *testing.T) {
	h, f := newHandler()
	w := httptest.NewRecorder()
	h.LogoutAll(w, authed(httptest.NewRequest("POST", "/api/v1/user/logout-all", nil)))
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body)
	}
	if len(f.sessionsRevoked) != 1 || f.sessionsRevoked[0] != "user-1" {
		t.Fatalf("RevokeSessions calls = %v", f.sessionsRevoked)
	}
	// PATs are untouched.
	if len(f.deleted) != 0 {
		t.Fatalf("deleted PATs = %v", f.deleted)
	}
	var cleared *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == authn.CookieName {
			cleared = c
		}
	}
	if cleared == nil || cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Fatalf("cookie = %+v, want cleared", cleared)
	}

	// Unauthenticated: 401, nothing revoked.
	w = httptest.NewRecorder()
	h.LogoutAll(w, httptest.NewRequest("POST", "/api/v1/user/logout-all", nil))
	if w.Code != http.StatusUnauthorized || len(f.sessionsRevoked) != 1 {
		t.Fatalf("anonymous: code %d, calls %v", w.Code, f.sessionsRevoked)
	}
}
