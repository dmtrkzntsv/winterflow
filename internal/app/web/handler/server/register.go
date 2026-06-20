package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

type registerServerRequest struct {
	Code           string `json:"code"`
	OrganizationID string `json:"organization_id,omitempty"`
}

// Register claims a pending server registration by its pairing code into the
// authenticated user's organization. The server-side resolves the org from the
// user's membership, so a client can't claim a server into an org it doesn't
// belong to.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	userID, err := util.GetUserID(r)
	if err != nil || userID == "" {
		util.Error(w, "failed to load user info", nil)
		return
	}

	var req registerServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		util.Error(w, "invalid request body", nil)
		return
	}
	if len(req.Code) != 6 {
		util.Error(w, "a 6-character code is required", nil)
		return
	}

	orgID, err := h.users.PrimaryOrganizationID(r.Context(), userID)
	if err != nil {
		util.Error(w, "failed to resolve organization", nil)
		return
	}
	// If the client named an org, it must be the one it belongs to.
	if req.OrganizationID != "" && req.OrganizationID != orgID {
		util.Error(w, "you don't have access to this organization", nil)
		return
	}

	srv, err := h.usecase.ClaimServer(r.Context(), req.Code, orgID)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrInvalidRegistrationCode):
			util.Error(w, "invalid registration code", nil)
		case errors.Is(err, model.ErrRegistrationCodeExpired):
			util.Error(w, "registration code has expired", nil)
		default:
			h.log.Error("failed to claim server", "error", err)
			util.Error(w, "failed to add server", nil)
		}
		return
	}

	util.Success(w, "server added", struct {
		Server model.Server `json:"server"`
	}{Server: srv})
}
