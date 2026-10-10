package notification

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
)

// Routes is the SSE stream's slice of the route table. Each event is
// "data: <Notification JSON>\n\n"; operation_result events carry the
// x-operation-result payload of the Accepted route that dispatched them.
func (h *Handler) Routes(api.Guards) []api.Route {
	return []api.Route{{
		Method: http.MethodGet, Path: "/api/v1/sse", Handler: h.Stream, Auth: api.User,
		Summary: "Notification stream (operation results, status changes)", Tag: "system",
		Events: model.Notification{},
	}}
}
