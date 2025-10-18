package server

import (
	"net/http"
	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

const serverIDKey = "server_id"

func (h *Handler) GetApps(w http.ResponseWriter, r *http.Request) {
	serverID := r.URL.Query().Get(serverIDKey)

	// @todo check ownership

	apps, err := h.appRepo.GetApps(serverID)
	if err != nil {
		util.Error(w, "failed to load servers", nil)
		return
	}
	util.Success(w, "", struct {
		Apps []model.App `json:"apps"`
	}{Apps: apps})
}

func GetAppsValidationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverID := r.URL.Query().Get(serverIDKey)
		if serverID == "" {
			util.Error(w, "serverID is required", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}
