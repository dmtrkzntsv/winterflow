package server

import (
	"net/http"
	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

// GetServersResponse is the data of GET /api/v1/server/get-servers.
type GetServersResponse struct {
	Servers []model.Server `json:"servers"`
}

func (h *Handler) GetServers(w http.ResponseWriter, r *http.Request) {
	userID, ok := util.RequireUser(w, r)
	if !ok {
		return
	}

	servers, err := h.servers.GetServers(r.Context(), userID)
	if err != nil {
		util.Error(w, "failed to load servers", nil)
		return
	}
	util.Success(w, "ok", GetServersResponse{Servers: servers})
}
