package util

import (
	"errors"
	"net/http"

	"github.com/go-pkgz/auth/v2/token"
)

// GetUserID returns the internal (DB) user id. It is stored as a claim
// attribute rather than in User.ID, because User.ID must keep the
// provider-prefixed form the auth middleware relies on. There is
// deliberately NO fallback to User.ID: every legitimate auth path
// (ClaimsUpd, PAT bearer, BasicAuthChecker) sets the attribute, so a
// missing one means the identity was never resolved to a DB user (e.g. a
// Google login while registration is closed) and must 401.
func GetUserID(r *http.Request) (string, error) {
	user, err := token.GetUserInfo(r)
	if err != nil {
		return "", err
	}
	if id := user.StrAttr("user_id"); id != "" {
		return id, nil
	}
	return "", errors.New("identity not linked to a user account")
}
