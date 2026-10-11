package org

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
)

// Routes is the org handler's slice of the route table. Every member of the
// org may call every route; each one acts on the caller's own org only.
func (h *Handler) Routes(api.Guards) []api.Route {
	bad := []int{http.StatusBadRequest}
	return []api.Route{
		{
			Method: http.MethodPost, Path: "/api/v1/org/create-user", Handler: h.CreateUser, Auth: api.User,
			Summary: "Create an account in the caller's org (temp password)", Tag: "org",
			Request: CreateUserRequest{}, Response: CreateUserResponse{}, Errors: bad,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/org/get-members", Handler: h.GetMembers, Auth: api.User,
			Summary: "List the caller's org members", Tag: "org",
			Response: []model.Member{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/remove-member", Handler: h.RemoveMember, Auth: api.User,
			Summary: "Remove a member and their account", Tag: "org",
			Request: MemberRequest{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/reset-member-password", Handler: h.ResetMemberPassword, Auth: api.User,
			Summary: "Issue a member a new temp password", Tag: "org",
			Request: MemberRequest{}, Response: ResetMemberPasswordResponse{}, Errors: bad,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/org/get-organization", Handler: h.GetOrganization, Auth: api.User,
			Summary: "The caller's org name, icon and color", Tag: "org",
			Response: model.Organization{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/update-organization", Handler: h.UpdateOrganization, Auth: api.User,
			Summary: "Rename or restyle the caller's org", Tag: "org",
			Request: UpdateOrganizationRequest{}, Errors: bad,
		},
	}
}
