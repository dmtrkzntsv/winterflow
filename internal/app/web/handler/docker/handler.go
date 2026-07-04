// Package docker exposes HTTP handlers for Docker resources (registries and
// networks). All operations are agent-bound: handlers validate, dispatch, and
// return 202 {request_id}; results arrive over SSE.
package docker

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/port"
	usedocker "winterflow/internal/domain/usecase/docker"
	"winterflow/pkg/logger"
)

type Handler struct {
	usecase usedocker.UseCase
}

type Deps struct {
	Logger            *logger.Logger
	CommandDispatcher port.CommandDispatcher
}

func NewHandler(d *Deps) *Handler {
	uc := usedocker.NewUseCase(&usedocker.Deps{
		CommandDispatcher: d.CommandDispatcher,
		Log:               d.Logger,
	})
	return &Handler{usecase: *uc}
}

func accepted(w http.ResponseWriter, msg, requestID string) {
	webutil.Accepted(w, msg, struct {
		RequestID string `json:"request_id"`
	}{RequestID: requestID})
}

// caller resolves the user id and the server_id query param, writing the error
// response itself when either is missing.
func caller(w http.ResponseWriter, r *http.Request) (userID, serverID string, ok bool) {
	userID, ok = webutil.RequireUser(w, r)
	if !ok {
		return "", "", false
	}
	serverID = r.URL.Query().Get("server_id")
	if serverID == "" {
		webutil.Error(w, "server_id is required", nil)
		return "", "", false
	}
	return userID, serverID, true
}

// --- registries ---

func (h *Handler) ListRegistries(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := caller(w, r)
	if !ok {
		return
	}
	reqID, err := h.usecase.ListRegistries(r.Context(), userID, serverID)
	if err != nil {
		webutil.Error(w, "failed to list registries", nil)
		return
	}
	accepted(w, "list registries dispatched", reqID)
}

func (h *Handler) CreateRegistry(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := caller(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[command.CreateRegistryRequest](w, r)
	if !ok {
		return
	}
	if req.Address == "" || req.Username == "" {
		webutil.Error(w, "address and username are required", nil)
		return
	}
	reqID, err := h.usecase.CreateRegistry(r.Context(), userID, serverID, req)
	if err != nil {
		webutil.Error(w, "failed to add registry", nil)
		return
	}
	accepted(w, "add registry dispatched", reqID)
}

func (h *Handler) DeleteRegistry(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := caller(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[command.DeleteRegistryRequest](w, r)
	if !ok {
		return
	}
	if req.Address == "" {
		webutil.Error(w, "address is required", nil)
		return
	}
	reqID, err := h.usecase.DeleteRegistry(r.Context(), userID, serverID, req.Address)
	if err != nil {
		webutil.Error(w, "failed to remove registry", nil)
		return
	}
	accepted(w, "remove registry dispatched", reqID)
}

// --- networks ---

func (h *Handler) ListNetworks(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := caller(w, r)
	if !ok {
		return
	}
	reqID, err := h.usecase.ListNetworks(r.Context(), userID, serverID)
	if err != nil {
		webutil.Error(w, "failed to list networks", nil)
		return
	}
	accepted(w, "list networks dispatched", reqID)
}

func (h *Handler) CreateNetwork(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := caller(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[command.CreateNetworkRequest](w, r)
	if !ok {
		return
	}
	if req.Name == "" {
		webutil.Error(w, "network name is required", nil)
		return
	}
	reqID, err := h.usecase.CreateNetwork(r.Context(), userID, serverID, req)
	if err != nil {
		webutil.Error(w, "failed to create network", nil)
		return
	}
	accepted(w, "create network dispatched", reqID)
}

// --- agent self-update ---

func (h *Handler) UpdateAgent(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := caller(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[command.UpdateAgentRequest](w, r)
	if !ok {
		return
	}
	if req.Version == "" {
		webutil.Error(w, "version is required", nil)
		return
	}
	reqID, err := h.usecase.UpdateAgent(r.Context(), userID, serverID, req.Version)
	if err != nil {
		webutil.Error(w, "failed to update agent", nil)
		return
	}
	accepted(w, "agent update dispatched", reqID)
}

func (h *Handler) DeleteNetwork(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := caller(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[command.DeleteNetworkRequest](w, r)
	if !ok {
		return
	}
	if req.Name == "" {
		webutil.Error(w, "network name is required", nil)
		return
	}
	reqID, err := h.usecase.DeleteNetwork(r.Context(), userID, serverID, req.Name)
	if err != nil {
		webutil.Error(w, "failed to remove network", nil)
		return
	}
	accepted(w, "remove network dispatched", reqID)
}
