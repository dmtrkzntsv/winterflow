package agenthub

import (
	"net/http"

	"winterflow/internal/app/web/api"
)

// HubPath is the agent hub route.
const HubPath = "/api/v1/agent/hub"

// Routes is the agent hub handler's slice of the route table. The route is
// unauthenticated (the agent's signature is its credential) and throttled
// per client IP by the enrollment polling limiter. It shares the "enroll"
// tag, so standalone (no hubs) drops it with the enrollment routes; the
// server also mounts it only where Deps.AgentHub is wired.
func (h *Handler) Routes(g api.Guards) []api.Route {
	return []api.Route{{
		Method: http.MethodPost, Path: HubPath, Handler: h.Where, Auth: api.Public,
		Middleware: []api.Middleware{g.EnrollLimit},
		Summary:    "Ask which hub an agent should connect to", Tag: "enroll",
		Description: "Signed by the agent: header `X-Winterflow-Signature` carries base64 of the ASN.1 " +
			"ECDSA signature, by the key of the server's active (unexpired) certificate, over SHA-256 of " +
			"`\"winterflow-agent-hub-v1\\n\" + server_id + \"\\n\" + timestamp + \"\\n\" + nonce`. The timestamp (Unix seconds) must be " +
			"within 5 minutes of the API's clock. When the server's hub is gone or draining, the " +
			"server is moved to another hub (`moved: true`). `hub_fingerprints` lists the lowercase hex " +
			"SHA-256 (of the DER) of every hub certificate the API trusts; the agent pins it. 503 when " +
			"no hub is available or the trusted set cannot be read (ask again).",
		Request: WhereRequest{}, Response: WhereResponse{},
		Errors: []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusServiceUnavailable},
	}}
}
