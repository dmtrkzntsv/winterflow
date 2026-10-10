package server

import (
	"errors"
	"net/http"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

// CheckDomainResponse is the data of GET /api/v1/domains/check. Claims lists
// the apps on the server already using the hostname (null when there are
// none).
type CheckDomainResponse struct {
	Available bool                `json:"available"`
	Claims    []model.DomainClaim `json:"claims"`
}

// CheckDomain answers "is this hostname free on this server?" for the app
// editor's live-typing validation. app_id (optional) excludes the app being
// edited so its own claims don't read as conflicts.
func (h *Handler) CheckDomain(w http.ResponseWriter, r *http.Request) {
	_, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	domain := r.URL.Query().Get("domain")
	claims, err := h.usecase.CheckDomain(r.Context(), serverID, domain, r.URL.Query().Get("app_id"))
	if err != nil {
		if errors.Is(err, model.ErrIngressInvalid) {
			webutil.Error(w, err.Error(), nil)
			return
		}
		webutil.Error(w, "failed to check domain", nil)
		return
	}
	webutil.Success(w, "", CheckDomainResponse{Available: len(claims) == 0, Claims: claims})
}
