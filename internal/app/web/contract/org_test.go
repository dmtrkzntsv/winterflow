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

	t.Run("members cannot administer", func(t *testing.T) {
		m.get(t, "/api/v1/org/get-members", nil).expect(t, http.StatusForbidden)
		m.get(t, "/api/v1/org/get-organization", nil).expect(t, http.StatusOK)
	})

	owner.post(t, "/api/v1/org/update-member", org.UpdateMemberRequest{UserID: m.userID, Role: "admin"}).expect(t, http.StatusOK)
	m.get(t, "/api/v1/org/get-members", nil).expect(t, http.StatusOK)
	t.Run("the last owner cannot be demoted", func(t *testing.T) {
		owner.post(t, "/api/v1/org/update-member", org.UpdateMemberRequest{UserID: owner.userID, Role: "member"}).
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
}
