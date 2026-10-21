package hubjoin

import (
	"net/http"

	"winterflow/internal/app/web/api"
)

// Routes is the hub join handler's slice of the route table. The route is
// unauthenticated (the token is the credential) and behind the tight
// per-IP limiter, since a hub joins rarely. routeTable mounts it only where
// a hub join usecase is wired; it shares the "enroll" tag with the agent
// enrollment routes, which are dropped from standalone the same way.
func (h *Handler) Routes(g api.Guards) []api.Route {
	return []api.Route{{
		Method: http.MethodPost, Path: "/api/v1/hub/enroll", Handler: h.Enroll, Auth: api.Public,
		Middleware: []api.Middleware{g.EnrollStartLimit},
		Summary:    "Redeem a one-time hub join token for the hub's server certificate", Tag: "enroll",
		Request: EnrollRequest{}, Response: EnrollResponse{},
		Errors: []int{http.StatusBadRequest, http.StatusTooManyRequests},
	}}
}
