package contract_test

import (
	"net/http"
	"testing"

	"winterflow/internal/app/web/handler/org"
	"winterflow/internal/domain/model"
)

func TestOrgRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)

	var o model.Organization
	owner.get(t, "/api/v1/org/get-organization", nil).expect(t, http.StatusOK).data(t, &o)
	if o.ID == "" {
		t.Fatalf("organization = %+v", o)
	}
	owner.post(t, "/api/v1/org/update-organization", org.UpdateOrganizationRequest{Name: "Acme", Icon: "box", Color: "#123456"}).
		expect(t, http.StatusOK)
	owner.get(t, "/api/v1/org/get-organization", nil).expect(t, http.StatusOK).data(t, &o)
	if o.Name != "Acme" || o.Icon != "box" || o.Color != "#123456" {
		t.Fatalf("organization after update = %+v", o)
	}

	m := owner.member(t)
	var members []model.Member
	owner.get(t, "/api/v1/org/get-members", nil).expect(t, http.StatusOK).data(t, &members)
	if len(members) != 2 {
		t.Fatalf("members = %+v", members)
	}

	// No roles: a member administers the org exactly like its creator.
	t.Run("members administer", func(t *testing.T) {
		m.get(t, "/api/v1/org/get-members", nil).expect(t, http.StatusOK)
		m.post(t, "/api/v1/org/update-organization", org.UpdateOrganizationRequest{Name: "Acme 2"}).
			expect(t, http.StatusOK)
		var created org.CreateUserResponse
		m.post(t, "/api/v1/org/create-user", org.CreateUserRequest{Name: "Carol", Email: "carol@example.test"}).
			expect(t, http.StatusOK).data(t, &created)
		m.post(t, "/api/v1/org/reset-member-password", org.MemberRequest{UserID: created.UserID}).
			expect(t, http.StatusOK)
		m.post(t, "/api/v1/org/remove-member", org.MemberRequest{UserID: created.UserID}).
			expect(t, http.StatusOK)
		m.post(t, "/api/v1/org/remove-member", org.MemberRequest{UserID: m.userID}).
			expect(t, http.StatusBadRequest)
	})

	var reset org.ResetMemberPasswordResponse
	owner.post(t, "/api/v1/org/reset-member-password", org.MemberRequest{UserID: m.userID}).
		expect(t, http.StatusOK).data(t, &reset)
	e.anon().login(t, m.email, reset.TempPassword)

	owner.post(t, "/api/v1/org/remove-member", org.MemberRequest{UserID: m.userID}).expect(t, http.StatusOK)
	owner.get(t, "/api/v1/org/get-members", nil).expect(t, http.StatusOK).data(t, &members)
	if len(members) != 1 {
		t.Fatalf("members after remove = %+v", members)
	}
	t.Run("cannot remove yourself", func(t *testing.T) {
		owner.post(t, "/api/v1/org/remove-member", org.MemberRequest{UserID: owner.userID}).expect(t, http.StatusBadRequest)
	})
	// No owner rule: a member may remove the org's creator, and the org
	// carries on with the member.
	t.Run("a member removes the org's creator", func(t *testing.T) {
		m2 := owner.member(t)
		m2.post(t, "/api/v1/org/remove-member", org.MemberRequest{UserID: owner.userID}).expect(t, http.StatusOK)
		m2.get(t, "/api/v1/org/get-members", nil).expect(t, http.StatusOK).data(t, &members)
		if len(members) != 1 || members[0].ID != m2.userID {
			t.Fatalf("members after removing the creator = %+v", members)
		}
		// The creator's account is gone: their password no longer logs in.
		e.anon().do(t, call{Method: http.MethodPost, Path: "/auth/local/login", Body: map[string]string{
			"user": owner.email, "passwd": ownerPassword, "aud": "contract",
		}}).expect(t, http.StatusForbidden)
	})
}

// Org boundaries: a member of one org cannot remove or reset a user of
// another org, and the target stays untouched.
func TestOrgMemberRoutesRefuseOtherOrgUsers(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)

	const otherEmail, otherPassword = "mallory@other.test", "password"
	other, err := e.deps.UserService.RegisterLocalUser(e.ctx, "Mallory", otherEmail, otherPassword)
	if err != nil {
		t.Fatal(err)
	}
	stillLogsIn := func(t *testing.T) {
		t.Helper()
		e.anon().do(t, call{Method: http.MethodPost, Path: "/auth/local/login", Body: map[string]string{
			"user": otherEmail, "passwd": otherPassword, "aud": "contract",
		}}).expect(t, http.StatusOK)
	}

	t.Run("remove-member", func(t *testing.T) {
		owner.post(t, "/api/v1/org/remove-member", org.MemberRequest{UserID: other.ID}).expect(t, http.StatusBadRequest)
		stillLogsIn(t)
	})
	t.Run("reset-member-password", func(t *testing.T) {
		owner.post(t, "/api/v1/org/reset-member-password", org.MemberRequest{UserID: other.ID}).expect(t, http.StatusBadRequest)
		stillLogsIn(t)
	})
}
