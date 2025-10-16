package server

import (
	"net/http"
	"winterflow/internal/app/controller/util"
)

func (h *Handler) GetServers(w http.ResponseWriter, r *http.Request) {
	util.Success(w, "ok", struct {
		Servers []string `json:"servers"`
	}{})
}
