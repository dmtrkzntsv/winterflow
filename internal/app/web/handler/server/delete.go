package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"winterflow/internal/app/web/util"
)

// revokeTimeout bounds the revocation publish.
const revokeTimeout = 10 * time.Second

// DeleteServerRequest is the body of POST /api/v1/server/delete-server.
type DeleteServerRequest struct {
	ServerID string `json:"server_id"`
}

// DeleteServer removes a server and its data from the API and tells the hubs
// to drop its agent. Containers on the box are not touched. A failed
// revocation publish is logged: the hub's connect-time check still denies the
// agent's next connection, since its certificate and row are gone. Refused
// with 409 where agents do not enroll by code (standalone).
func (h *Handler) DeleteServer(w http.ResponseWriter, r *http.Request) {
	userID, ok := util.RequireUser(w, r)
	if !ok {
		return
	}
	var req DeleteServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ServerID == "" {
		util.Error(w, "server_id is required", nil)
		return
	}
	if !util.RequireServerAccess(w, r, h.servers, userID, req.ServerID) {
		return
	}
	// Without code enrollment (standalone) the server is the embedded agent,
	// which could never be added back.
	if h.claims == nil {
		util.ErrorStatus(w, http.StatusConflict, "this instance's own server can't be deleted")
		return
	}
	if err := h.servers.DeleteServer(r.Context(), req.ServerID); err != nil {
		h.log.Error("delete server failed", "server_id", req.ServerID, "error", err)
		util.Error(w, "failed to delete the server", nil)
		return
	}
	if h.status != nil {
		h.status.Forget(req.ServerID)
	}
	if h.latest != nil {
		h.latest.Forget(req.ServerID)
	}
	if h.revoker != nil {
		// Detached from the request: the server is already gone.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), revokeTimeout)
		defer cancel()
		if err := h.revoker.Revoke(ctx, req.ServerID); err != nil {
			h.log.Error("revoke server failed", "server_id", req.ServerID, "error", err)
		}
	}
	util.Success(w, "ok", nil)
}
