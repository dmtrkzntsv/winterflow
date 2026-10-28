package oauthlogin

import (
	"net/http"

	"winterflow/internal/app/web/api"
)

// Routes is the provider sign-in slice of the route table. The browser
// routes answer redirects; they verify nothing priced, but each start
// stores a row and each callback calls the provider, so they sit behind
// g.AuthLimit.
func (h *Handler) Routes(g api.Guards) []api.Route {
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/auth/oauth/{provider}/start", Handler: h.Start, Auth: api.Public,
			Middleware: []api.Middleware{g.AuthLimit}, Redirect: http.StatusFound,
			Summary: "Start a sign-in (or a link) with a provider: redirects to it", Tag: "auth",
			Description: "Sets the `wf_oauth_flow` cookie the callback checks. `mode=link` needs the session cookie.",
			Query:       StartQuery{},
			Errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound,
				http.StatusTooManyRequests, http.StatusInternalServerError},
		},
		{
			Method: http.MethodGet, Path: "/auth/oauth/{provider}/callback", Handler: h.Callback, Auth: api.Public,
			Middleware: []api.Middleware{g.AuthLimit}, Redirect: http.StatusFound,
			Summary: "The provider's answer: signs in (sets wf_session) or links, then redirects", Tag: "auth",
			Description: "Redirects to the start's `return_to`, or on failure to `/login?error=<code>` " +
				"(a link: to `return_to` with `?error=<code>`).",
			Query:  CallbackQuery{},
			Errors: []int{http.StatusBadRequest, http.StatusTooManyRequests},
		},
		{
			Method: http.MethodPost, Path: "/auth/oauth/{provider}/callback", Handler: h.CallbackPost, Auth: api.Public,
			Middleware: []api.Middleware{g.AuthLimit}, Redirect: http.StatusSeeOther,
			Summary: "A form_post answer (Apple): re-sent to the GET callback", Tag: "auth",
			Query: CallbackPath{}, Form: CallbackForm{},
			Errors: []int{http.StatusBadRequest, http.StatusTooManyRequests},
		},
		{
			Method: http.MethodGet, Path: "/api/v1/user/get-identities", Handler: h.GetIdentities, Auth: api.User,
			Summary: "List the caller's linked sign-in provider accounts", Tag: "auth",
			Response: []IdentityItem{}, Errors: []int{http.StatusBadRequest},
		},
		{
			Method: http.MethodPost, Path: "/api/v1/user/unlink-identity", Handler: h.UnlinkIdentity, Auth: api.User,
			Summary: "Unlink a sign-in provider account", Tag: "auth",
			Description: "409 when it is the only way into the account (no password and no other linked provider).",
			Request:     UnlinkIdentityRequest{},
			Errors:      []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict},
		},
	}
}
