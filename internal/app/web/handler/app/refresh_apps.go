package server

import (
	"net/http"
	webutil "winterflow/internal/app/web/util"
)

// RefreshApps asks the server's agent for its actual deployed apps and
// reconciles the DB cache. Fire-and-forward: returns 202 with a request_id; the
// reconciled list is delivered over SSE. The UI calls this on the apps view to
// re-sync against the agent (the source of truth).
func (h *Handler) RefreshApps(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}

	serverID := r.URL.Query().Get(serverIDKey)
	if serverID == "" {
		webutil.Error(w, "server_id is required", nil)
		return
	}

	// @todo check ownership of server_id by userID

	requestID, err := h.usecase.RefreshApps(r.Context(), userID, serverID)
	if err != nil {
		webutil.Error(w, "failed to refresh apps", nil)
		return
	}

	webutil.Accepted(w, "apps refresh dispatched", struct {
		RequestID string `json:"request_id"`
	}{RequestID: requestID})
}
