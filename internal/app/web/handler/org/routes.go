package org

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
)

// Routes is the org handler's slice of the route table. Everything except
// get-organization is admin-only via g.Admin.
func (h *Handler) Routes(g api.Guards) []api.Route {
	admin := []api.Middleware{g.Admin}
	// 403 comes from the admin guard.
	adminErrs := []int{http.StatusBadRequest, http.StatusForbidden}
	return []api.Route{
		{
			Method: http.MethodPost, Path: "/api/v1/org/create-user", Handler: h.CreateUser, Auth: api.User, Middleware: admin,
			Summary: "Create an account in the caller's org (temp password)", Tag: "org",
			Request: CreateUserRequest{}, Response: CreateUserResponse{}, Errors: adminErrs,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/org/get-members", Handler: h.GetMembers, Auth: api.User, Middleware: admin,
			Summary: "List the caller's org members", Tag: "org",
			Response: []model.Member{}, Errors: adminErrs,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/update-member", Handler: h.UpdateMember, Auth: api.User, Middleware: admin,
			Summary: "Change a member's role", Tag: "org",
			Request: UpdateMemberRequest{}, Errors: adminErrs,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/remove-member", Handler: h.RemoveMember, Auth: api.User, Middleware: admin,
			Summary: "Remove a member and their account", Tag: "org",
			Request: MemberRequest{}, Errors: adminErrs,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/reset-member-password", Handler: h.ResetMemberPassword, Auth: api.User, Middleware: admin,
			Summary: "Issue a member a new temp password", Tag: "org",
			Request: MemberRequest{}, Response: ResetMemberPasswordResponse{}, Errors: adminErrs,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/org/get-organization", Handler: h.GetOrganization, Auth: api.User,
			Summary: "The caller's org name, icon and color", Tag: "org",
			Response: model.Organization{}, Errors: []int{http.StatusBadRequest},
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/update-organization", Handler: h.UpdateOrganization, Auth: api.User, Middleware: admin,
			Summary: "Rename or restyle the caller's org", Tag: "org",
			Request: UpdateOrganizationRequest{}, Errors: adminErrs,
		},
	}
}
