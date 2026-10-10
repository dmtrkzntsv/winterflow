package auth

import (
	"net/http"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/app/web/api"
)

// LoginRequest is the JSON body of POST /auth/local/login (go-pkgz direct
// provider). user is the account email; aud is the audience (the UI's
// hostname).
type LoginRequest struct {
	User   string `json:"user"`
	Passwd string `json:"passwd"`
	Aud    string `json:"aud"`
}

// LoginForm is the form-encoded alternative the web UI sends.
type LoginForm struct {
	User   string `formData:"user"`
	Passwd string `formData:"passwd"`
	Aud    string `formData:"aud"`
}

// LoginQuery: sess=1 issues a session-only cookie.
type LoginQuery struct {
	Sess string `query:"sess" enum:"0,1"`
}

// SessionUser is the go-pkgz user record (login and /auth/user responses).
type SessionUser token.User

// AuthStatus is the body of GET /auth/status.
type AuthStatus struct {
	Status string `json:"status" enum:"logged in,not logged in"`
	User   string `json:"user,omitempty"`
}

// AvatarPath names a stored avatar file.
type AvatarPath struct {
	Avatar string `path:"avatar"`
}

// Routes documents the go-pkgz endpoints mounted at /auth (behind the auth
// rate limiter) and /avatar. They are Mounted: served by the library, so
// api.Register skips them. Responses are the library's own JSON, not the
// webutil envelope (Raw); its errors are {"error": "..."}. Google OAuth
// (/auth/google/*) exists only when configured and is not listed.
func Routes() []api.Route {
	limited := []int{http.StatusTooManyRequests}
	return []api.Route{
		{
			Method: http.MethodPost, Path: "/auth/local/login", Auth: api.Public, Mounted: true, Raw: true,
			Summary: "Email+password login; sets the JWT cookie", Tag: "auth",
			Query: LoginQuery{}, Request: LoginRequest{}, Form: LoginForm{}, Response: SessionUser{},
			Errors: []int{http.StatusBadRequest, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError},
		},
		{
			Method: http.MethodGet, Path: "/auth/logout", Auth: api.Public, Mounted: true, Raw: true,
			Summary: "Clear the JWT cookie", Tag: "auth", Errors: limited,
		},
		{
			Method: http.MethodGet, Path: "/auth/status", Auth: api.Public, Mounted: true, Raw: true,
			Summary: "Whether the request carries a valid session", Tag: "auth",
			Response: AuthStatus{}, Errors: limited,
		},
		{
			Method: http.MethodGet, Path: "/auth/user", Auth: api.Public, Mounted: true, Raw: true,
			Summary: "The session's user record", Tag: "auth",
			Response: SessionUser{}, Errors: []int{http.StatusUnauthorized, http.StatusTooManyRequests},
		},
		{
			Method: http.MethodGet, Path: "/auth/list", Auth: api.Public, Mounted: true, Raw: true,
			Summary: "Names of the enabled login providers", Tag: "auth",
			Response: []string{}, Errors: limited,
		},
		{
			Method: http.MethodGet, Path: "/avatar/{avatar}", Auth: api.Public, Mounted: true, Raw: true,
			Summary: "A stored avatar image", Tag: "auth",
			Query: AvatarPath{}, ContentType: "image/*",
			Errors: []int{http.StatusBadRequest, http.StatusForbidden},
		},
	}
}
