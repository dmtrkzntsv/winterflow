package user

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
)

// Routes is the user handler's slice of the route table. change-password
// verifies and hashes a password and set-password hashes one, so both sit
// behind g.AuthLimit.
func (h *Handler) Routes(g api.Guards) []api.Route {
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
			Summary: "The caller's profile", Tag: "user",
			Response: Profile{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/user/change-password", Handler: h.ChangePassword, Auth: api.User,
			Middleware: []api.Middleware{g.AuthLimit},
			Summary:    "Change the caller's local password", Tag: "user",
			Request: ChangePasswordRequest{}, Errors: []int{http.StatusBadRequest, http.StatusTooManyRequests},
		},
		{
			Method: http.MethodPost, Path: "/api/v1/user/set-password", Handler: h.SetPassword, Auth: api.User,
			Middleware: []api.Middleware{g.AuthLimit},
			Summary:    "Give an account without a password (made through a sign-in provider) its first one", Tag: "user",
			Description: "No current password is asked. `email` must be an address one of the caller's linked sign-in " +
				"providers vouched for, and no other account may use it (409); an account that has a password gets a 409.",
			Request: SetPasswordRequest{}, Errors: []int{http.StatusBadRequest, http.StatusConflict, http.StatusTooManyRequests, http.StatusInternalServerError},
		},
		{
			Method: http.MethodPost, Path: "/api/v1/user/logout-all", Handler: h.LogoutAll, Auth: api.User,
			Summary: "Sign out everywhere: revoke every session of the caller and clear the cookie", Tag: "user",
			Description: "Personal access tokens and connected OAuth apps keep working.",
			Errors:      []int{http.StatusInternalServerError},
		},
	}
}
