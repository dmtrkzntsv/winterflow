package util

import (
	"context"
	"net/http"

	"winterflow/internal/domain/model"
)

// principalKey is the request-context key of the authenticated principal.
type principalKey struct{}

// WithPrincipal returns r carrying p as its authenticated principal: what
// the auth middleware does once a credential checks out (and what handler
// tests do to stand in for it).
func WithPrincipal(r *http.Request, p model.Principal) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
}

// Principal returns the request's authenticated principal; false on a
// request the auth middleware did not authenticate.
func Principal(r *http.Request) (model.Principal, bool) {
	p, ok := r.Context().Value(principalKey{}).(model.Principal)
	return p, ok
}
