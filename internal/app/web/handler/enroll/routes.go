package enroll

import (
	"net/http"

	"winterflow/internal/app/web/api"
)

// Routes is the enroll handler's slice of the route table. The routes are
// unauthenticated (the agent has no account) and throttled per client IP;
// routeTable mounts them only where an enrollment usecase is wired.
func (h *Handler) Routes(g api.Guards) []api.Route {
	mw := []api.Middleware{g.AuthLimit}
	errs := []int{http.StatusBadRequest}
	return []api.Route{
		{
			Method: http.MethodPost, Path: "/api/v1/agent/enroll", Handler: h.Start, Auth: api.Public, Middleware: mw,
			Summary: "Start an agent enrollment: submit a CSR, get a pairing code", Tag: "enroll",
			Request: StartRequest{}, Response: CodeResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusServiceUnavailable},
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
			Request: PollRequest{}, Response: StatusResponse{}, Errors: errs,
		},
	}
}
