package user

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

type fakeTokens struct {
	created  []model.UserToken
	deleted  []string
	deleteBy string
}

func (f *fakeTokens) CreateToken(_ context.Context, userID, name string, expiresAt *time.Time) (model.UserToken, string, error) {
	rec := model.UserToken{ID: "tok-1", UserID: userID, Name: name, Prefix: "wfp_abcd1234", ExpiresAt: expiresAt, CreatedAt: time.Now()}
	f.created = append(f.created, rec)
	return rec, "wfp_abcd1234SECRETSECRETSECRETSECRETSECRET", nil
}
func (f *fakeTokens) ListTokens(_ context.Context, userID string) ([]model.UserToken, error) {
	return f.created, nil
}
func (f *fakeTokens) DeleteToken(_ context.Context, userID, tokenID string) error {
	f.deleteBy = userID
	if tokenID != "tok-1" {
		return model.ErrTokenNotFound
	}
	f.deleted = append(f.deleted, tokenID)
	return nil
}

func authed(r *http.Request) *http.Request {
	u := token.User{ID: "user-1"}
	u.SetStrAttr("user_id", "user-1")
	return token.SetUserInfo(r, u)
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
	_, _, _ = f.CreateToken(context.Background(), "user-1", "ci", nil)
	r := authed(httptest.NewRequest("GET", "/x", nil))
	w := httptest.NewRecorder()
	h.GetTokens(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "SECRET") {
		t.Error("list response leaks plaintext")
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
	role       string
	creds      *model.Credentials
	verifyErr  error
	setCalls   []bool // mustChange flag per SetPassword call
	setUserIDs []string
}

func (f *fakeProfileStore) GetUser(_ context.Context, userID string) (model.User, error) {
	return model.User{ID: userID, Name: "Alice"}, nil
}
func (f *fakeProfileStore) RoleOf(context.Context, string) (string, error) { return f.role, nil }
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
	f := &fakeProfileStore{role: "owner", creds: &model.Credentials{Email: "a@b.io", MustChangePassword: true}}
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
			Role               string `json:"role"`
			Email              string `json:"email"`
			MustChangePassword bool   `json:"must_change_password"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Role != "owner" || resp.Data.Email != "a@b.io" || !resp.Data.MustChangePassword {
		t.Errorf("profile = %+v", resp.Data)
	}
}

func TestGetProfileGoogleOnlyHasNoEmail(t *testing.T) {
	h := newProfileHandler(&fakeProfileStore{role: "member", creds: nil})
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
	r = authed(httptest.NewRequest("POST", "/x", strings.NewReader(`{"current_password":"old","new_password":"short"}`)))
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
