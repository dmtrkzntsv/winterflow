// Package patauth authenticates personal access tokens sent as
// "Authorization: Bearer wfp_…". Anything else falls through to the wrapped
// JWT middleware, so browser sessions are untouched. (PATs in Basic auth are
// handled separately by the go-pkgz BasicAuthChecker in web/bootstrap.)
package patauth

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-pkgz/auth/v2/token"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/pkg/pat"
)

// Authenticator resolves a raw access token (the authn usecase).
type Authenticator interface {
	Authenticate(ctx context.Context, raw string) (model.Principal, error)
}

// AuthenticatePAT resolves raw to the user it acts for, accepting only a
// personal access token: model.ErrUnauthorized for anything else (an OAuth
// token is for /mcp only).
func AuthenticatePAT(ctx context.Context, a Authenticator, raw string) (string, error) {
	p, err := a.Authenticate(ctx, raw)
	if err != nil {
		return "", err
	}
	if p.Kind != model.TokenPAT {
		return "", model.ErrUnauthorized
	}
	return p.UserID, nil
}

const bearerPAT = "Bearer " + pat.TokenPrefix

// Middleware returns an auth middleware: PAT bearer requests are resolved
// against the DB; everything else is delegated to jwtAuth (go-pkgz).
func Middleware(tokens Authenticator, jwtAuth func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		viaJWT := jwtAuth(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, bearerPAT) {
				viaJWT.ServeHTTP(w, r)
				return
			}
			userID, err := AuthenticatePAT(r.Context(), tokens, strings.TrimPrefix(auth, "Bearer "))
			if err != nil {
				webutil.Unauthorized(w)
				return
			}
			// Mirror the claims shape the JWT path produces: the internal user
			// id lives in the "user_id" attribute (read by util.GetUserID).
			u := token.User{ID: userID}
			u.SetStrAttr("user_id", userID)
			next.ServeHTTP(w, token.SetUserInfo(r, u))
		})
	}
}
