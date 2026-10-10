package notification

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// StreamDescription documents the delivery semantics of GET /api/v1/sse.
// They follow from notification.Manager: a per-user fan-out with a
// one-slot, non-blocking buffer per open stream and no history.
const StreamDescription = "Server-sent events, each `data: <Notification JSON>`.\n\n" +
	"Subscribe BEFORE dispatching a 202 operation: results are best-effort, not buffered or replayed. " +
	"A result produced while no stream is open, or while this stream is not being read, is dropped. " +
	"An operation_result is delivered only to the open streams of the user who dispatched it; " +
	"match it on ref == the 202's data.request_id. server_status and apps_status events go to every " +
	"member of the server's organization and interleave with results; ignore types you do not handle."

// Routes is the SSE stream's slice of the route table. operation_result
// events carry the x-operation-result payload of the Accepted route that
// dispatched them; the status events carry the payloads listed here.
func (h *Handler) Routes(api.Guards) []api.Route {
	return []api.Route{{
		Method: http.MethodGet, Path: "/api/v1/sse", Handler: h.Stream, Auth: api.User,
		Summary: "Notification stream (operation results, status changes)", Tag: "system",
		Description: StreamDescription,
		Events:      model.Notification{},
		EventPayloads: []api.EventPayload{
			{Type: string(model.NotificationOperationResult)},
			{Type: string(model.NotificationServerStatus), Payload: model.ServerStatusPayload{}},
			{Type: string(model.NotificationAppsStatus), Payload: command.AppsStatusPayload{}},
		},
	}}
}
