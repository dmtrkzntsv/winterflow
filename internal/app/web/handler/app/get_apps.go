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

	apps, err := h.usecase.GetApps(r.Context(), serverID)
	if err != nil {
		util.Error(w, "failed to load servers", nil)
		return
	}
	domains, err := h.usecase.ListDomains(r.Context(), serverID)
	if err != nil {
		// Chips are decoration; the listing must not fail over the index.
		// Still worth a log line so a stale/broken app_domains index doesn't
		// go unnoticed.
		if h.log != nil {
			h.log.Warn("GetApps: list domains", "error", err, "server_id", serverID)
		}
		domains = nil
	}
	type appWithDomains struct {
		model.App
		Domains []model.AppDomainInfo `json:"domains,omitempty"`
	}
	out := make([]appWithDomains, 0, len(apps))
	for _, a := range apps {
		out = append(out, appWithDomains{App: a, Domains: domains[a.ID]})
	}
	util.Success(w, "", struct {
		Apps []appWithDomains `json:"apps"`
	}{Apps: out})
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
