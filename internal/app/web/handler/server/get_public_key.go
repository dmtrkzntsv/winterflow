package server

import (
	"net/http"

	"winterflow/internal/app/web/util"
	"winterflow/pkg/crypto"
)

// GetPublicKey returns the server/agent EC public key (base64 of the
// uncompressed P-256 point) so the browser can ECIES-encrypt app secrets before
// sending them. The agent decrypts them with its matching private key.
//
// Source order: the persisted `public_key` capability (published by the agent,
// works for both topologies), falling back to the local agent certificate
// (standalone, where the API shares disk with the agent).
func (h *Handler) GetPublicKey(w http.ResponseWriter, r *http.Request) {
	serverID := r.URL.Query().Get("server_id")
	if serverID == "" {
		util.Error(w, "server_id is required", nil)
		return
	}

	// @todo check ownership of server_id by the authenticated user.

	if h.servers != nil {
		if value, ok, err := h.servers.GetCapability(r.Context(), serverID, "public_key"); err == nil && ok && value != "" {
			util.Success(w, "ok", publicKeyResponse{PublicKey: value})
			return
		}
	}

	// Standalone fallback: read the embedded agent's certificate from disk.
	if h.cfg != nil {
		if point, err := crypto.PublicKeyPointFromCertPath(h.cfg.GetAgentCertPath()); err == nil {
			util.Success(w, "ok", publicKeyResponse{PublicKey: point})
			return
		} else {
			h.log.Debug("public key from cert failed", "error", err)
		}
	}

	util.Error(w, "public key unavailable for this server", nil)
}

type publicKeyResponse struct {
	PublicKey string `json:"public_key"`
}
