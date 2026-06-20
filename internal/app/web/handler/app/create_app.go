package server

import (
	"encoding/json"
	"net/http"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	useapp "winterflow/internal/domain/usecase/app"
	"winterflow/pkg/util"

	"github.com/go-chi/chi/v5/middleware"
)

type createAppRequest struct {
	ServerID string    `json:"server_id"`
	App      model.App `json:"app"`
}

// CreateApp accepts an app definition, dispatches an app.save command to the
// target agent (through the usecase -> AppService -> Bus), and returns
// immediately. The eventual result is delivered to the caller over the SSE
// notification stream, keyed by request id.
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

	requestID := middleware.GetReqID(r.Context())
	if requestID == "" {
		requestID = util.GenerateID()
	}

	app := req.App
	app.ServerID = req.ServerID

	// The usecase reads userID and requestID from context to route the async
	// notification back to this caller.
	ctx := useapp.WithUserID(r.Context(), userID)
	ctx = useapp.WithRequestID(ctx, requestID)

	if err := h.usecase.CreateApp(ctx, req.ServerID, app); err != nil {
		webutil.Error(w, "failed to create app", nil)
		return
	}

	webutil.Success(w, "app creation dispatched", struct {
		RequestID string `json:"request_id"`
	}{RequestID: requestID})
}
