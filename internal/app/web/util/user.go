package util

import (
	"net/http"

	"github.com/go-pkgz/auth/v2/token"
)

// GetUserID returns the internal (DB) user id. It is stored as a claim
// attribute rather than in User.ID, because User.ID must keep the
// provider-prefixed form the auth middleware relies on. Falls back to User.ID
// for safety if the attribute is absent.
func GetUserID(r *http.Request) (string, error) {
	user, err := token.GetUserInfo(r)
	if err != nil {
		return "", err
	}

	if id := user.StrAttr("user_id"); id != "" {
		return id, nil
	}
	return user.ID, nil
}
