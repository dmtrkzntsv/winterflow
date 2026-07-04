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
