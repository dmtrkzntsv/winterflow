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
	org        model.Organization
	updatedOrg string
	members    []model.Member
	created    []string
	removed    []string
	resetPw    []string
	takenEmail string
}

func (f *fakeOrgStore) PrimaryOrganizationID(context.Context, string) (string, error) {
	return "org-1", nil
}
func (f *fakeOrgStore) CreateMemberUser(_ context.Context, orgID, name, email, tempPassword string) (model.User, error) {
	if email == f.takenEmail {
		return model.User{}, model.ErrEmailTaken
	}
	f.created = append(f.created, email+"|"+tempPassword)
	return model.User{ID: "new-1", Name: name}, nil
}
func (f *fakeOrgStore) ListMembers(context.Context, string) ([]model.Member, error) {
	return f.members, nil
}
func (f *fakeOrgStore) RemoveMember(_ context.Context, _, userID string) error {
	if userID == "not-in-org" {
		return model.ErrorUserNotFound
	}
	f.removed = append(f.removed, userID)
	return nil
}
func (f *fakeOrgStore) SetPassword(_ context.Context, userID, _ string, mustChange bool) error {
	f.resetPw = append(f.resetPw, userID)
	return nil
}
func (f *fakeOrgStore) GetOrganization(context.Context, string) (model.Organization, error) {
	return f.org, nil
}
func (f *fakeOrgStore) UpdateOrganization(_ context.Context, _, name, icon, color string) error {
	f.updatedOrg = name + "|" + icon + "|" + color
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
	w := post(h.CreateUser, `{"name":"Bob","email":"Bob@X.io"}`)
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
	if len(f.created) != 1 || f.created[0] != "bob@x.io|"+resp.Data.TempPassword {
		t.Errorf("created = %v", f.created)
	}
	if strings.Contains(w.Body.String(), `"role"`) {
		t.Errorf("response still carries a role: %s", w.Body.String())
	}
}

// A client that still sends a role (a browser tab opened before the
// upgrade) creates the user anyway: the field is ignored, whatever it says.
func TestCreateUserIgnoresStaleRole(t *testing.T) {
	h, f := newHandler()
	for i, role := range []string{"member", "admin", "owner", "weird"} {
		body := `{"name":"B","email":"b` + string(rune('0'+i)) + `@x.io","role":"` + role + `"}`
		if w := post(h.CreateUser, body); w.Code != http.StatusOK {
			t.Errorf("role %q: code = %d, want 200", role, w.Code)
		}
	}
	if len(f.created) != 4 {
		t.Errorf("created = %v, want 4 accounts", f.created)
	}
}

func TestCreateUserValidation(t *testing.T) {
	h, f := newHandler()
	f.takenEmail = "taken@x.io"
	for _, body := range []string{
		`{}`,
		`{"name":"B","email":""}`,
		`{"name":"B","email":"not-an-email"}`,
		`{"name":"B","email":"taken@x.io"}`,
	} {
		if w := post(h.CreateUser, body); w.Code != http.StatusBadRequest {
			t.Errorf("body %s: code = %d, want 400", body, w.Code)
		}
	}
}

func TestRemoveMember(t *testing.T) {
	h, f := newHandler()
	// Caller is admin-1; removing self is refused (it would lock them out).
	if w := post(h.RemoveMember, `{"user_id":"admin-1"}`); w.Code != http.StatusBadRequest {
		t.Errorf("self-remove: code = %d, want 400", w.Code)
	}
	if w := post(h.RemoveMember, `{"user_id":""}`); w.Code != http.StatusBadRequest {
		t.Errorf("empty user_id: code = %d, want 400", w.Code)
	}
	// A user outside the caller's org is a 400, not a removal.
	if w := post(h.RemoveMember, `{"user_id":"not-in-org"}`); w.Code != http.StatusBadRequest {
		t.Errorf("foreign user: code = %d, want 400", w.Code)
	}
	// Any other member goes, whoever created the org: there is no owner.
	if w := post(h.RemoveMember, `{"user_id":"m-1"}`); w.Code != http.StatusOK {
		t.Errorf("remove member: code = %d", w.Code)
	}
	if len(f.removed) != 1 || f.removed[0] != "m-1" {
		t.Errorf("removed = %v, want [m-1]", f.removed)
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
	f.members = []model.Member{{User: model.User{ID: "u-1", Name: "Ann"}, Email: "ann@x.io", Provider: "local"}}
	r := authed(httptest.NewRequest("GET", "/x", nil))
	w := httptest.NewRecorder()
	h.GetMembers(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ann@x.io") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"role"`) {
		t.Errorf("members still carry a role: %s", w.Body.String())
	}
}

func TestGetOrganization(t *testing.T) {
	h, f := newHandler()
	f.org = model.Organization{ID: "org-1", Name: "Homelab", Icon: "server", Color: "#3b82f6"}
	r := authed(httptest.NewRequest("GET", "/x", nil))
	w := httptest.NewRecorder()
	h.GetOrganization(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	var resp struct {
		Data model.Organization `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Name != "Homelab" || resp.Data.Icon != "server" || resp.Data.Color != "#3b82f6" {
		t.Errorf("org = %+v", resp.Data)
	}
}

func TestUpdateOrganizationValidation(t *testing.T) {
	h, f := newHandler()
	for _, body := range []string{
		`{"name":"","icon":"server","color":"#fff"}`,
		`{"name":"` + strings.Repeat("x", 65) + `","icon":"","color":""}`,
		`{"name":"ok","icon":"","color":"not-a-color-way-too-long"}`,
	} {
		if w := post(h.UpdateOrganization, body); w.Code != http.StatusBadRequest {
			t.Errorf("body %s: code = %d, want 400", body, w.Code)
		}
	}
	if w := post(h.UpdateOrganization, `{"name":"Homelab","icon":"server","color":"#3b82f6"}`); w.Code != http.StatusOK {
		t.Errorf("valid update: code = %d", w.Code)
	}
	if f.updatedOrg != "Homelab|server|#3b82f6" {
		t.Errorf("updatedOrg = %q", f.updatedOrg)
	}
}
