package server

import (
	"net/http"
	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

func (h *Handler) GetServers(w http.ResponseWriter, r *http.Request) {
	userID, err := util.GetUserID(r)
	if err != nil {
		util.Error(w, "failed to load user info", err)
		return
	}

	servers, err := h.usecase.GetServers(r.Context(), userID)
	if err != nil {
		util.Error(w, "failed to load servers", nil)
		return
	}
	util.Success(w, "ok", struct {
		Servers []model.Server `json:"servers"`
	}{Servers: servers})
}
