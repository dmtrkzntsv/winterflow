package contract_test

import (
	"net/http"
	"strings"
	"testing"

	"winterflow/internal/app/web/handler/user"
	"winterflow/internal/domain/model"
)

func TestUserRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)

	t.Run("profile", func(t *testing.T) {
		var p user.Profile
		r := owner.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusOK)
		r.data(t, &p)
		if p.UserID != owner.userID || p.Email != owner.email || p.MustChangePassword {
			t.Fatalf("profile = %+v", p)
		}
		if strings.Contains(string(r.Body), `"role"`) {
			t.Fatalf("profile still carries a role: %s", r.Body)
		}
	})

	t.Run("tokens: create, authenticate, list, delete", func(t *testing.T) {
		var created user.CreateTokenResponse
		owner.post(t, "/api/v1/user/create-token", user.CreateTokenRequest{Name: "ci", ExpiresInDays: 30}).
			expect(t, http.StatusOK).data(t, &created)
		if created.Token == "" || created.ExpiresAt == nil {
			t.Fatalf("created = %+v", created)
		}
		bearer := e.newClient()
		bearer.bearer = created.Token
		var p user.Profile
		bearer.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusOK).data(t, &p)
		if p.UserID != owner.userID {
			t.Fatalf("PAT resolves to %q, want %q", p.UserID, owner.userID)
		}

		var tokens []model.UserToken
		owner.get(t, "/api/v1/user/get-tokens", nil).expect(t, http.StatusOK).data(t, &tokens)
		if len(tokens) != 1 || tokens[0].ID != created.TokenID || tokens[0].LastUsedAt == nil {
			t.Fatalf("tokens = %+v", tokens)
		}

		owner.post(t, "/api/v1/user/delete-token", user.DeleteTokenRequest{TokenID: created.TokenID}).expect(t, http.StatusOK)
		bearer.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusUnauthorized)
		owner.get(t, "/api/v1/user/get-tokens", nil).expect(t, http.StatusOK).data(t, &tokens)
		if len(tokens) != 0 {
			t.Fatalf("tokens after delete = %+v", tokens)
		}
	})

	t.Run("change password", func(t *testing.T) {
		owner.post(t, "/api/v1/user/change-password", user.ChangePasswordRequest{
			CurrentPassword: "wrong", NewPassword: "new-password",
		}).expect(t, http.StatusBadRequest)
		owner.post(t, "/api/v1/user/change-password", user.ChangePasswordRequest{
			CurrentPassword: ownerPassword, NewPassword: "new-password",
		}).expect(t, http.StatusOK)
		e.anon().login(t, owner.email, "new-password")
		e.anon().do(t, call{Method: http.MethodPost, Path: "/auth/local/login", Body: map[string]string{
			"user": owner.email, "passwd": ownerPassword, "aud": "contract",
		}}).expect(t, http.StatusForbidden)
	})
}
