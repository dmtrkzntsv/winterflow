package server

import (
	"errors"
	"net/http"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

// CheckDomain answers "is this hostname free?" for the app editor's
// live-typing validation. app_id (optional) excludes the app being edited so
// its own claims don't read as conflicts.
func (h *Handler) CheckDomain(w http.ResponseWriter, r *http.Request) {
	if _, ok := webutil.RequireUser(w, r); !ok {
		return
	}
	domain := r.URL.Query().Get("domain")
	claims, err := h.usecase.CheckDomain(r.Context(), domain, r.URL.Query().Get("app_id"))
	if err != nil {
		if errors.Is(err, model.ErrIngressInvalid) {
			webutil.Error(w, err.Error(), nil)
			return
		}
		webutil.Error(w, "failed to check domain", nil)
		return
	}
	webutil.Success(w, "", struct {
		Available bool                `json:"available"`
		Claims    []model.DomainClaim `json:"claims"`
	}{Available: len(claims) == 0, Claims: claims})
}
