package user

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
)

// Routes is the user handler's slice of the route table.
func (h *Handler) Routes(api.Guards) []api.Route {
	bad := []int{http.StatusBadRequest}
	return []api.Route{
		{
			Method: http.MethodPost, Path: "/api/v1/user/create-token", Handler: h.CreateToken, Auth: api.User,
			Summary: "Mint a personal access token", Tag: "user",
			Request: CreateTokenRequest{}, Response: CreateTokenResponse{}, Errors: bad,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/user/get-tokens", Handler: h.GetTokens, Auth: api.User,
			Summary: "List the caller's personal access tokens", Tag: "user",
			Response: []model.UserToken{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/user/delete-token", Handler: h.DeleteToken, Auth: api.User,
			Summary: "Revoke a personal access token", Tag: "user",
			Request: DeleteTokenRequest{}, Errors: bad,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/user/get-profile", Handler: h.GetProfile, Auth: api.User,
			Summary: "The caller's profile and role", Tag: "user",
			Response: Profile{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/user/change-password", Handler: h.ChangePassword, Auth: api.User,
			Summary: "Change the caller's local password", Tag: "user",
			Request: ChangePasswordRequest{}, Errors: bad,
		},
	}
}
