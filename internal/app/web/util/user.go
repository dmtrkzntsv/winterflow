package util

import (
	"errors"
	"net/http"
)

// errNoPrincipal is GetUserID's answer on a request the auth middleware did
// not authenticate.
var errNoPrincipal = errors.New("request is not authenticated")

// GetUserID returns the authenticated user's id, which the auth middleware
// keeps in the request context (see WithPrincipal). A request without one
// (a public route, or a handler test that set none) is an error.
func GetUserID(r *http.Request) (string, error) {
	p, ok := Principal(r)
	if !ok || p.UserID == "" {
		return "", errNoPrincipal
	}
	return p.UserID, nil
}
