package org

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

type fakeOrgStore struct {
	members    []model.Member
	created    []string
	updated    map[string]string
	removed    []string
	resetPw    []string
	takenEmail string
	lastOwner  string
}

func (f *fakeOrgStore) PrimaryOrganizationID(context.Context, string) (string, error) {
	return "org-1", nil
}
func (f *fakeOrgStore) CreateMemberUser(_ context.Context, orgID, name, email, role, tempPassword string) (model.User, error) {
	if email == f.takenEmail {
		return model.User{}, model.ErrEmailTaken
	}
	f.created = append(f.created, email+"|"+role+"|"+tempPassword)
	return model.User{ID: "new-1", Name: name}, nil
}
func (f *fakeOrgStore) ListMembers(context.Context, string) ([]model.Member, error) {
	return f.members, nil
}
func (f *fakeOrgStore) UpdateMemberRole(_ context.Context, _, userID, role string) error {
	if userID == f.lastOwner {
		return model.ErrLastOwner
	}
	if f.updated == nil {
		f.updated = map[string]string{}
	}
	f.updated[userID] = role
	return nil
}
func (f *fakeOrgStore) RemoveMember(_ context.Context, _, userID string) error {
	if userID == f.lastOwner {
		return model.ErrLastOwner
	}
	f.removed = append(f.removed, userID)
	return nil
}
func (f *fakeOrgStore) SetPassword(_ context.Context, userID, _ string, mustChange bool) error {
	f.resetPw = append(f.resetPw, userID)
	return nil
}
func (f *fakeOrgStore) GetCredentials(_ context.Context, userID string) (model.Credentials, error) {
	if userID == "google-only" {
		return model.Credentials{}, model.ErrorUserNotFound
	}
	return model.Credentials{Email: "x@y.io"}, nil
}

func authed(r *http.Request) *http.Request {
	u := token.User{ID: "admin-1"}
	u.SetStrAttr("user_id", "admin-1")
	return token.SetUserInfo(r, u)
}

func newHandler() (*Handler, *fakeOrgStore) {
	f := &fakeOrgStore{}
	return NewHandler(&Deps{
		Logger: logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
		Users:  f,
	}), f
}

func post(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	r := authed(httptest.NewRequest("POST", "/x", strings.NewReader(body)))
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestCreateUserReturnsTempPasswordOnce(t *testing.T) {
	h, f := newHandler()
	w := post(h.CreateUser, `{"name":"Bob","email":"Bob@X.io","role":"member"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Email        string `json:"email"`
			TempPassword string `json:"temp_password"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Email != "bob@x.io" {
		t.Errorf("email = %q, want normalized", resp.Data.Email)
	}
	if len(resp.Data.TempPassword) != 16 {
		t.Errorf("temp password len = %d, want 16", len(resp.Data.TempPassword))
	}
	if len(f.created) != 1 || !strings.HasPrefix(f.created[0], "bob@x.io|member|") {
		t.Errorf("created = %v", f.created)
	}
}

func TestCreateUserValidation(t *testing.T) {
	h, f := newHandler()
	f.takenEmail = "taken@x.io"
	for _, body := range []string{
		`{}`,
		`{"name":"B","email":"","role":"member"}`,
		`{"name":"B","email":"not-an-email","role":"member"}`,
		`{"name":"B","email":"b@x.io","role":"owner"}`,
		`{"name":"B","email":"b@x.io","role":"weird"}`,
		`{"name":"B","email":"taken@x.io","role":"member"}`,
	} {
		if w := post(h.CreateUser, body); w.Code != http.StatusBadRequest {
			t.Errorf("body %s: code = %d, want 400", body, w.Code)
		}
	}
}

func TestUpdateMemberLastOwnerGuard(t *testing.T) {
	h, f := newHandler()
	f.lastOwner = "owner-1"
	if w := post(h.UpdateMember, `{"user_id":"owner-1","role":"member"}`); w.Code != http.StatusBadRequest {
		t.Errorf("last owner demote: code = %d, want 400", w.Code)
	}
	if w := post(h.UpdateMember, `{"user_id":"m-1","role":"admin"}`); w.Code != http.StatusOK {
		t.Errorf("promote: code = %d", w.Code)
	}
}

func TestRemoveMemberSelfForbidden(t *testing.T) {
	h, _ := newHandler()
	// Caller is admin-1; removing self is refused.
	if w := post(h.RemoveMember, `{"user_id":"admin-1"}`); w.Code != http.StatusBadRequest {
		t.Errorf("self-remove: code = %d, want 400", w.Code)
	}
	if w := post(h.RemoveMember, `{"user_id":"m-1"}`); w.Code != http.StatusOK {
		t.Errorf("remove member: code = %d", w.Code)
	}
}

func TestResetMemberPassword(t *testing.T) {
	h, f := newHandler()
	w := post(h.ResetMemberPassword, `{"user_id":"m-1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	var resp struct {
		Data struct {
			TempPassword string `json:"temp_password"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data.TempPassword) != 16 {
		t.Errorf("temp password len = %d", len(resp.Data.TempPassword))
	}
	if len(f.resetPw) != 1 {
		t.Errorf("SetPassword calls = %d", len(f.resetPw))
	}
	// Google-only member (no credentials) → 400.
	if w := post(h.ResetMemberPassword, `{"user_id":"google-only"}`); w.Code != http.StatusBadRequest {
		t.Errorf("google-only reset: code = %d, want 400", w.Code)
	}
}

func TestGetMembers(t *testing.T) {
	h, f := newHandler()
	f.members = []model.Member{{Role: "owner"}}
	r := authed(httptest.NewRequest("GET", "/x", nil))
	w := httptest.NewRecorder()
	h.GetMembers(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "owner") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}
