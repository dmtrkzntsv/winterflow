package server

import (
	"net/http"
	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

const serverIDKey = "server_id"

// AppWithDomains is one app in the get-apps listing: the catalog record plus
// the hostnames its ingress claims (omitted when it claims none).
type AppWithDomains struct {
	model.App
	Domains []model.AppDomainInfo `json:"domains,omitempty"`
}

// GetAppsResponse is the data of GET /api/v1/app/get-apps.
type GetAppsResponse struct {
	Apps []AppWithDomains `json:"apps"`
}

func (h *Handler) GetApps(w http.ResponseWriter, r *http.Request) {
	serverID := r.URL.Query().Get(serverIDKey)
	if _, ok := h.authorize(w, r, serverID); !ok {
		return
	}

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
	out := make([]AppWithDomains, 0, len(apps))
	for _, a := range apps {
		out = append(out, AppWithDomains{App: a, Domains: domains[a.ID]})
	}
	util.Success(w, "", GetAppsResponse{Apps: out})
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
