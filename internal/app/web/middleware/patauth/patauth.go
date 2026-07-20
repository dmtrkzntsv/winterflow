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

// TokenResolver is the slice of port.UserService this middleware needs.
type TokenResolver interface {
	FindByToken(ctx context.Context, token string) (model.User, error)
}

const bearerPAT = "Bearer " + pat.TokenPrefix

// Middleware returns an auth middleware: PAT bearer requests are resolved
// against the DB; everything else is delegated to jwtAuth (go-pkgz).
func Middleware(users TokenResolver, jwtAuth func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		viaJWT := jwtAuth(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, bearerPAT) {
				viaJWT.ServeHTTP(w, r)
				return
			}
			user, err := users.FindByToken(r.Context(), strings.TrimPrefix(auth, "Bearer "))
			if err != nil {
				webutil.Unauthorized(w)
				return
			}
			// Mirror the claims shape the JWT path produces: the internal user
			// id lives in the "user_id" attribute (read by util.GetUserID).
			u := token.User{ID: user.ID, Name: user.Name}
			u.SetStrAttr("user_id", user.ID)
			next.ServeHTTP(w, token.SetUserInfo(r, u))
		})
	}
}
