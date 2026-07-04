package rbac

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-pkgz/auth/v2/token"
)

type fakeRoles struct{ role string }

func (f *fakeRoles) RoleOf(context.Context, string) (string, error) { return f.role, nil }

func run(t *testing.T, role string, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	h := RequireAdmin(&fakeRoles{role: role})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest("POST", "/api/v1/org/create-user", nil)
	if authed {
		u := token.User{ID: "u1"}
		u.SetStrAttr("user_id", "u1")
		r = token.SetUserInfo(r, u)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAdminAndOwnerPass(t *testing.T) {
	for _, role := range []string{"owner", "admin"} {
		if w := run(t, role, true); w.Code != http.StatusOK {
			t.Errorf("role %s: code = %d, want 200", role, w.Code)
		}
	}
}

func TestMemberForbidden(t *testing.T) {
	if w := run(t, "member", true); w.Code != http.StatusForbidden {
		t.Errorf("member: code = %d, want 403", w.Code)
	}
}

func TestUnauthenticatedIs401(t *testing.T) {
	if w := run(t, "owner", false); w.Code != http.StatusUnauthorized {
		t.Errorf("code = %d, want 401", w.Code)
	}
}
