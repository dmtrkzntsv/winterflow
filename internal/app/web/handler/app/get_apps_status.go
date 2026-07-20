package server

import (
	"net/http"
	"time"
	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/command"
)

// GetAppsStatus returns the cached container status for a server's apps (live,
// in-memory, TTL'd). Empty/expired reads as no status (unknown). The agent
// pushes status via events; this endpoint serves the latest snapshot.
func (h *Handler) GetAppsStatus(w http.ResponseWriter, r *http.Request) {
	serverID := r.URL.Query().Get(serverIDKey)

	// @todo check ownership

	statuses := h.status.AppStatuses(serverID, time.Now())
	if statuses == nil {
		statuses = []command.AppStatus{}
	}
	util.Success(w, "ok", struct {
		Apps []command.AppStatus `json:"apps"`
	}{Apps: statuses})
}
