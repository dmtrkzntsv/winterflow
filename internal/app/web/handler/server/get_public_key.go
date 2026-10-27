package server

import (
	"net/http"

	"winterflow/internal/app/web/util"
)

// GetPublicKey returns the server/agent EC public key (base64 of the
// uncompressed P-256 point) so the browser can ECIES-encrypt app secrets before
// sending them. The agent decrypts them with its matching private key.
//
// Source order: the persisted `public_key` capability (published by the agent,
// works for both topologies), falling back to the local agent certificate
// (standalone, where the API shares disk with the agent).
func (h *Handler) GetPublicKey(w http.ResponseWriter, r *http.Request) {
	userID, ok := util.RequireUser(w, r)
	if !ok {
		return
	}
	serverID := r.URL.Query().Get("server_id")
	if serverID == "" {
		util.Error(w, "server_id is required", nil)
		return
	}
	if !util.RequireServerAccess(w, r, h.servers, userID, serverID) {
		return
	}

	var caps util.CapabilityReader
	if h.servers != nil {
		caps = h.servers
	}
	point, err := util.ServerPublicKey(r.Context(), caps, h.cfg, serverID)
	if err == nil {
		util.Success(w, "ok", PublicKeyResponse{PublicKey: point})
		return
	}
	h.log.Debug("public key from cert failed", "error", err)
	util.Error(w, "public key unavailable for this server", nil)
}

// PublicKeyResponse is the data of GET /api/v1/server/get-public-key: the
// agent's P-256 public point, base64.
type PublicKeyResponse struct {
	PublicKey string `json:"public_key"`
}
