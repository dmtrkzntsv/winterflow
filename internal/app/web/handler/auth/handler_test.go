package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func newHandler(t *testing.T, mode string, users int) (*Handler, *fakeReg) {
	t.Helper()
	f := &fakeReg{users: users}
	return NewHandler(&Deps{
		Logger: logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
		Cfg:    config.NewServerConfig(mode),
		Users:  f,
	}), f
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
	if w.Header().Get("Set-Cookie") != "" {
		t.Error("register must not issue a session")
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
