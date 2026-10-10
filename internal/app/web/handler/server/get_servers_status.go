package server

import (
	"net/http"
	"time"
	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/service/status"
)

// GetServersStatusResponse is the data of GET /api/v1/server/get-servers-status.
type GetServersStatusResponse struct {
	Servers []status.ServerStatus `json:"servers"`
}

// GetServersStatus returns live liveness (from the in-memory TTL cache) for the
// user's servers. Status is never persisted; absence/expiry reads as "unknown".
func (h *Handler) GetServersStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := util.RequireUser(w, r)
	if !ok {
		return
	}

	servers, err := h.servers.GetServers(r.Context(), userID)
	if err != nil {
		util.Error(w, "failed to load servers", nil)
		return
	}

	now := time.Now()
	out := make([]status.ServerStatus, 0, len(servers))
	for _, s := range servers {
		out = append(out, status.ServerStatus{
			ServerID: s.ID,
			Liveness: h.status.ServerLiveness(s.ID, now),
		})
	}
	util.Success(w, "ok", GetServersStatusResponse{Servers: out})
}
