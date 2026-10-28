package auth

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/app/web/middleware/authn"
)

// Routes is the auth handler's slice of the route table. Login and register
// verify or hash a password, so they sit behind g.AuthLimit; they set the
// session cookie, so they also require X-Winterflow-Request (login CSRF).
func (h *Handler) Routes(g api.Guards) []api.Route {
	return []api.Route{
		{
			Method: http.MethodPost, Path: "/api/v1/auth/login", Handler: h.Login, Auth: api.Public,
			Middleware: []api.Middleware{authn.RequireAppRequest, g.AuthLimit},
			Summary:    "Sign in with email and password; sets the wf_session cookie", Tag: "auth",
			Description: "The same 401 answers an unknown email and a wrong password. " +
				"Must send `X-Winterflow-Request: 1` (403 without), as must every cookie-authenticated request that changes state.",
			Request: LoginRequest{}, Response: LoginResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError},
		},
		{
			Method: http.MethodPost, Path: "/api/v1/auth/logout", Handler: h.Logout, Auth: api.User,
			Summary: "Sign out: revoke the presenting session and clear the cookie", Tag: "auth",
			Errors: []int{http.StatusInternalServerError},
		},
		{
			Method: http.MethodPost, Path: "/api/v1/auth/register", Handler: h.Register, Auth: api.Public,
			Middleware: []api.Middleware{authn.RequireAppRequest, g.AuthLimit},
			Summary:    "Create an account and sign it in (first call claims a fresh instance)", Tag: "auth",
			Description: "Must send `X-Winterflow-Request: 1` (403 without).",
			Request:     RegisterRequest{}, Response: RegisterResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError},
		},
		{
			Method: http.MethodGet, Path: "/api/v1/auth/state", Handler: h.State, Auth: api.Public,
			Summary: "Whether the instance is fresh, registration is open, and which sign-in providers exist", Tag: "auth",
			Response: StateResponse{}, Errors: []int{http.StatusBadRequest},
		},
	}
}
