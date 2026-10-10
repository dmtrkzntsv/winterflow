package auth

import (
	"net/http"

	"winterflow/internal/app/web/api"
)

// Routes is the public auth handler's slice of the route table. Register is
// bcrypt-priced, so it sits behind g.AuthLimit.
func (h *Handler) Routes(g api.Guards) []api.Route {
	return []api.Route{
		{
			Method: http.MethodPost, Path: "/api/v1/auth/register", Handler: h.Register, Auth: api.Public,
			Middleware: []api.Middleware{g.AuthLimit},
			Summary:    "Create an account (first call claims a fresh instance)", Tag: "auth",
			Request: RegisterRequest{}, Response: RegisterResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusTooManyRequests},
		},
		{
			Method: http.MethodGet, Path: "/api/v1/auth/state", Handler: h.State, Auth: api.Public,
			Summary: "Whether the instance is fresh and registration is open", Tag: "auth",
			Response: StateResponse{}, Errors: []int{http.StatusBadRequest},
		},
	}
}
