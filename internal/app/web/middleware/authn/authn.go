// Package authn is the HTTP side of opaque-token authentication: it finds
// the request's credential, resolves it through the authn usecase and keeps
// the principal in the request context (read by webutil.GetUserID and
// webutil.Principal).
//
// A credential is looked for in this order: "Authorization: Bearer <token>",
// "Authorization: Basic" with the token as the password (the username is
// ignored), then the session cookie. Only the first one present is tried.
//
// CSRF: the browser sends the session cookie on its own, so a request
// authenticated by the cookie with a method other than GET, HEAD or OPTIONS
// must carry the header "X-Winterflow-Request: 1", which a cross-site form
// or a simple cross-origin request cannot set; without it the answer is 403.
// Bearer and Basic requests carry their credential explicitly and are
// exempt.
package authn

import (
	"context"
	"net/http"
	"strings"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

const (
	// CookieName is the browser session cookie.
	CookieName = "wf_session"
	// CSRFHeader must be "1" on a cookie-authenticated unsafe request.
	CSRFHeader = "X-Winterflow-Request"
)

// Authenticator resolves a raw access token (the authn usecase).
type Authenticator interface {
	Authenticate(ctx context.Context, raw string) (model.Principal, error)
}

// Options configures Middleware.
type Options struct {
	// AllowOAuth admits OAuth (wfo_) tokens; only the /mcp chain sets it.
	AllowOAuth bool
	// Challenge, when set, is sent as WWW-Authenticate on every 401.
	Challenge string
}

// Middleware authenticates every request it wraps, answering 401 (no
// credential, or one that does not authenticate) or 403 (a cookie-borne
// unsafe request without the CSRF header) itself.
func Middleware(a Authenticator, opts Options) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deny := func() {
				if opts.Challenge != "" {
					w.Header().Set("WWW-Authenticate", opts.Challenge)
				}
				webutil.Unauthorized(w)
			}
			raw, byCookie := credential(r)
			if raw == "" {
				deny()
				return
			}
			p, err := a.Authenticate(r.Context(), raw)
			if err != nil {
				deny()
				return
			}
			// Allowlist the kinds: an OAuth token only where admitted, and
			// an unknown kind nowhere. The answer is the same 401 as a bad
			// token, so a token does not reveal that it is valid elsewhere.
			switch p.Kind {
			case model.TokenSession, model.TokenPAT:
			case model.TokenOAuth:
				if !opts.AllowOAuth {
					deny()
					return
				}
			default:
				deny()
				return
			}
			if byCookie && !safeMethod(r.Method) && r.Header.Get(CSRFHeader) != "1" {
				webutil.Forbidden(w)
				return
			}
			next.ServeHTTP(w, webutil.WithPrincipal(r, p))
		})
	}
}

// credential returns the request's raw token and whether it came from the
// session cookie; "" when the request carries none.
func credential(r *http.Request) (raw string, byCookie bool) {
	if h := r.Header.Get("Authorization"); h != "" {
		const bearer = "bearer "
		if len(h) > len(bearer) && strings.EqualFold(h[:len(bearer)], bearer) {
			return strings.TrimSpace(h[len(bearer):]), false
		}
		if _, pw, ok := r.BasicAuth(); ok {
			return pw, false
		}
		// An Authorization header of another scheme is not a credential;
		// it does not fall back to the cookie either.
		return "", false
	}
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		return c.Value, true
	}
	return "", false
}

// safeMethod reports the methods that must not change state.
func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}
