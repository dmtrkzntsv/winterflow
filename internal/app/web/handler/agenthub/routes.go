package agenthub

import (
	"net/http"

	"winterflow/internal/app/web/api"
)

// Routes is the agent hub handler's slice of the route table. The route is
// unauthenticated (the agent's signature is its credential) and throttled
// per client IP by the enrollment polling limiter. It shares the "enroll"
// tag, so standalone (no hubs) drops it with the enrollment routes.
func (h *Handler) Routes(g api.Guards) []api.Route {
	return []api.Route{{
		Method: http.MethodPost, Path: "/api/v1/agent/hub", Handler: h.Where, Auth: api.Public,
		Middleware: []api.Middleware{g.EnrollLimit},
		Summary:    "Ask which hub an agent should connect to", Tag: "enroll",
		Description: "Signed by the agent: header `X-Winterflow-Signature` carries base64 of the ASN.1 " +
			"ECDSA signature, by the key of the server's active certificate, over SHA-256 of " +
			"`server_id + \"\\n\" + timestamp + \"\\n\" + nonce`. The timestamp (Unix seconds) must be " +
			"within 5 minutes of the API's clock. When the server's hub is gone or draining, the " +
			"server is moved to another hub (`moved: true`).",
		Request: WhereRequest{}, Response: WhereResponse{},
		Errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusServiceUnavailable},
	}}
}
