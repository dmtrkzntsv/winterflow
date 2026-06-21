package server

import (
	"encoding/json"
	"net/http"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

type createAppRequest struct {
	ServerID string    `json:"server_id"`
	App      model.App `json:"app"`
}

// CreateApp accepts an app definition and dispatches an app.save command to the
// target agent (fire-and-forward). It returns 202 immediately with a
// request_id; the result is delivered to the caller over the SSE notification
// stream, correlated by that request_id.
func (h *Handler) CreateApp(w http.ResponseWriter, r *http.Request) {
	userID, err := webutil.GetUserID(r)
	if err != nil || userID == "" {
		webutil.Error(w, "unauthorized", nil)
		return
	}

	var req createAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webutil.Error(w, "invalid request body", nil)
		return
	}
	if req.ServerID == "" {
		webutil.Error(w, "server_id is required", nil)
		return
	}

	// @todo check ownership of server_id by userID

	app := req.App
	app.ServerID = req.ServerID

	requestID, err := h.usecase.CreateApp(r.Context(), userID, req.ServerID, app)
	if err != nil {
		webutil.Error(w, "failed to create app", nil)
		return
	}

	webutil.Accepted(w, "app creation dispatched", struct {
		RequestID string `json:"request_id"`
	}{RequestID: requestID})
}
