package server

import (
	"net/http"
	"time"
	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/service/status"
)

type serverStatusDTO struct {
	ServerID string          `json:"server_id"`
	Liveness status.Liveness `json:"liveness"`
}

// GetServersStatus returns live liveness (from the in-memory TTL cache) for the
// user's servers. Status is never persisted; absence/expiry reads as "unknown".
func (h *Handler) GetServersStatus(w http.ResponseWriter, r *http.Request) {
	userID, err := util.GetUserID(r)
	if err != nil || userID == "" {
		util.Error(w, "failed to load user info", nil)
		return
	}

	servers, err := h.usecase.GetServers(r.Context(), userID)
	if err != nil {
		util.Error(w, "failed to load servers", nil)
		return
	}

	now := time.Now()
	out := make([]serverStatusDTO, 0, len(servers))
	for _, s := range servers {
		out = append(out, serverStatusDTO{
			ServerID: s.ID,
			Liveness: h.status.ServerLiveness(s.ID, now),
		})
	}
	util.Success(w, "ok", struct {
		Servers []serverStatusDTO `json:"servers"`
	}{Servers: out})
}
