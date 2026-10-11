package server

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	useapp "winterflow/internal/domain/usecase/app"
)

// CheckDomainResponse is the data of GET /api/v1/domains/check. Available:
// no other app on the server claims the hostname (Claims lists those that
// do, null when none). Covered: a domain registered on the server covers it
// (Registration is the one that wins, exact over wildcard).
type CheckDomainResponse struct {
	Available    bool                    `json:"available"`
	Covered      bool                    `json:"covered"`
	Registration *model.RegisteredDomain `json:"registration,omitempty"`
	Claims       []model.DomainClaim     `json:"claims"`
}

// CheckDomain answers "is this hostname free on this server?" for the app
// editor's live-typing validation. app_id (optional) excludes the app being
// edited so its own claims don't read as conflicts.
//
// The hostname's syntax is checked first: live typing sends many partial
// names, and rejecting them costs no DB query. It reveals nothing about
// claims, which stay behind the ownership check.
func (h *Handler) CheckDomain(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if err := useapp.ValidateCheckDomain(domain); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	_, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	check, err := h.usecase.CheckDomain(r.Context(), serverID, domain, r.URL.Query().Get("app_id"))
	if err != nil {
		webutil.Error(w, "failed to check domain", nil)
		return
	}
	webutil.Success(w, "", CheckDomainResponse{
		Available:    len(check.Claims) == 0,
		Covered:      check.Registration != nil,
		Registration: check.Registration,
		Claims:       check.Claims,
	})
}
