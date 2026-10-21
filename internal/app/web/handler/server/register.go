package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/hubs"
)

// RegisterServerRequest is the body of POST /api/v1/server/register.
type RegisterServerRequest struct {
	Code           string `json:"code"`
	OrganizationID string `json:"organization_id,omitempty"`
}

// RegisterServerResponse is the data of POST /api/v1/server/register.
type RegisterServerResponse struct {
	Server model.Server `json:"server"`
}

// Register claims a pending server registration by its pairing code into the
// authenticated user's organization. The server-side resolves the org from the
// user's membership, so a client can't claim a server into an org it doesn't
// belong to.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	userID, ok := util.RequireUser(w, r)
	if !ok {
		return
	}

	var req RegisterServerRequest
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

	if !h.claimLimit.Allow(userID, h.now()) {
		util.ErrorStatus(w, http.StatusTooManyRequests, "too many attempts, wait a minute")
		return
	}

	srv, err := h.claim(r.Context(), req.Code, orgID)
	if err != nil {
		switch {
		case errors.Is(err, hubs.ErrNoHub):
			util.ErrorStatus(w, http.StatusServiceUnavailable, hubs.ErrNoHub.Error())
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

	util.Success(w, "server added", RegisterServerResponse{Server: srv})
}

// claim tries an enrolled agent's code first; a code no enrollment holds
// falls back to the registration rows (standalone's embedded agent).
func (h *Handler) claim(ctx context.Context, code, orgID string) (model.Server, error) {
	if h.claims != nil {
		srv, err := h.claims.Claim(ctx, code, orgID)
		if !errors.Is(err, model.ErrInvalidRegistrationCode) {
			return srv, err
		}
	}
	return h.servers.ClaimServer(ctx, dto.ClaimServerDTO{Code: code, OrganizationID: orgID})
}
