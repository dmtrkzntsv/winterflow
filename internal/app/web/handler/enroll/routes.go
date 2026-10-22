package enroll

import (
	"net/http"

	"winterflow/internal/app/web/api"
)

// Routes is the enroll handler's slice of the route table. The routes are
// unauthenticated (the agent has no account) and throttled per client IP:
// Start, which stores a row, by its own tight limiter; refresh and status,
// which an installer polls every few seconds, by the polling one. routeTable
// mounts them only where an enrollment usecase is wired.
func (h *Handler) Routes(g api.Guards) []api.Route {
	mw := []api.Middleware{g.EnrollLimit}
	return []api.Route{
		{
			Method: http.MethodPost, Path: "/api/v1/agent/enroll", Handler: h.Start, Auth: api.Public,
			Middleware: []api.Middleware{g.EnrollStartLimit},
			Summary:    "Start an agent enrollment: submit a CSR, get a pairing code", Tag: "enroll",
			Request: StartRequest{}, Response: CodeResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusServiceUnavailable},
		},
		{
			Method: http.MethodPost, Path: "/api/v1/agent/enroll/refresh", Handler: h.Refresh, Auth: api.Public, Middleware: mw,
			Summary: "Replace the code of a pending enrollment", Tag: "enroll",
			Request: PollRequest{}, Response: CodeResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusConflict},
		},
		{
			Method: http.MethodPost, Path: "/api/v1/agent/enroll/status", Handler: h.Status, Auth: api.Public, Middleware: mw,
			Summary: "Poll an enrollment; once claimed it carries the signed certificate", Tag: "enroll",
			Description: "Once claimed, `hub_fingerprints` lists the lowercase hex SHA-256 (of the DER) of " +
				"every hub certificate the API trusts; the agent pins it. 503 when that set cannot be read (poll again).",
			Request: PollRequest{}, Response: StatusResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusServiceUnavailable},
		},
	}
}
