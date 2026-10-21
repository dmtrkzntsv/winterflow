// Package agenthub serves the public route an agent asks where to connect
// at: it signs the request with its certificate key and gets its hub's
// address, moved to another hub when its own is gone or draining.
package agenthub

import (
	"context"
	"errors"
	"net/http"
	"time"

	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/usecase/agenthub"
	"winterflow/internal/domain/usecase/hubs"
	"winterflow/pkg/agentsig"
	"winterflow/pkg/logger"
)

const (
	// maxBody bounds the request body.
	maxBody = 4 << 10
	// maxField bounds server_id and nonce.
	maxField = 128
	// whereTimeout bounds the hub list read and the database work.
	whereTimeout = 10 * time.Second
)

// Locator is the slice of *agenthub.UseCase the route uses.
type Locator interface {
	Where(ctx context.Context, serverID string, ts int64, nonce, sig string) (addr string, moved bool, err error)
}

type Handler struct {
	uc  Locator
	log *logger.Logger
}

type Deps struct {
	Logger  *logger.Logger
	Locator Locator
}

func NewHandler(d *Deps) *Handler { return &Handler{uc: d.Locator, log: d.Logger} }

// WhereRequest is the body of POST /api/v1/agent/hub; the signature over
// it rides the X-Winterflow-Signature header. Timestamp is Unix seconds.
type WhereRequest struct {
	ServerID  string `json:"server_id"`
	Timestamp int64  `json:"timestamp"`
	Nonce     string `json:"nonce"`
}

// WhereResponse is the data of the route: the hub to connect to, and
// whether the server was just moved there.
type WhereResponse struct {
	HubAddr string `json:"hub_addr"`
	Moved   bool   `json:"moved"`
}

// Where handles POST /api/v1/agent/hub.
func (h *Handler) Where(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	req, ok := util.DecodeBody[WhereRequest](w, r)
	if !ok {
		return
	}
	if req.ServerID == "" || req.Nonce == "" || len(req.ServerID) > maxField || len(req.Nonce) > maxField {
		util.Error(w, "server_id and nonce are required (at most 128 bytes each)", nil)
		return
	}
	sig := r.Header.Get(agentsig.Header)
	if sig == "" {
		util.ErrorStatus(w, http.StatusUnauthorized, "missing signature")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), whereTimeout)
	defer cancel()
	addr, moved, err := h.uc.Where(ctx, req.ServerID, req.Timestamp, req.Nonce, sig)
	switch {
	case err == nil:
		util.Success(w, "ok", WhereResponse{HubAddr: addr, Moved: moved})
	case errors.Is(err, agenthub.ErrUnauthorized):
		util.ErrorStatus(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, agenthub.ErrStaleRequest):
		util.Error(w, err.Error(), nil)
	case errors.Is(err, hubs.ErrNoHub):
		w.Header().Set("Retry-After", "60")
		util.ErrorStatus(w, http.StatusServiceUnavailable, hubs.ErrNoHub.Error())
	default:
		h.log.Error("failed to answer the agent's hub", "error", err, "server_id", req.ServerID)
		util.ErrorStatus(w, http.StatusInternalServerError, "failed to find a hub")
	}
}
