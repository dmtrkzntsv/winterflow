package server

import (
	"net/http"
	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

func (h *Handler) GetServers(w http.ResponseWriter, r *http.Request) {
	servers, _ := h.serverRepo.GetServers()
	util.Success(w, "ok", struct {
		Servers []model.Server `json:"servers"`
	}{Servers: servers})
}
