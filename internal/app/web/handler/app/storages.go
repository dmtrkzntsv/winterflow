package server

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// GetStoragesResponse is the data of GET /api/v1/storage/get-storages.
type GetStoragesResponse struct {
	Storages []model.StorageSummary `json:"storages"`
}

// GetStorages answers the server's storages from the API's index
// (synchronous, no agent round-trip, never a secret). The index is filled
// when the agent comes online and kept current by storage.save/delete and
// storage.list.
func (h *Handler) GetStorages(w http.ResponseWriter, r *http.Request) {
	_, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	list, err := h.usecase.ServerStorages(r.Context(), serverID)
	if err != nil {
		webutil.Error(w, "failed to load storages", nil)
		return
	}
	webutil.Success(w, "", GetStoragesResponse{Storages: list})
}

// ListStorages dispatches storage.list; the storages (with the apps using
// each) arrive over SSE and refresh the server_storages index.
func (h *Handler) ListStorages(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	reqID, err := h.usecase.ListServerStorages(r.Context(), userID, serverID)
	if err != nil {
		webutil.Error(w, "failed to list storages", nil)
		return
	}
	accepted(w, "list storages dispatched", reqID)
}

// TestStorage asks the agent to write and delete a probe object; the
// outcome ({ok, error}) arrives over SSE. Nothing is stored.
func (h *Handler) TestStorage(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[command.TestStorageRequest](w, r)
	if !ok {
		return
	}
	if err := req.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	reqID, err := h.usecase.TestStorage(r.Context(), userID, serverID, req)
	if err != nil {
		webutil.Error(w, "failed to test storage", nil)
		return
	}
	accepted(w, "test storage dispatched", reqID)
}

// SaveStorage adds or replaces a storage; the agent tests it first and
// redeploys the running apps that reference it.
func (h *Handler) SaveStorage(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[command.SaveStorageRequest](w, r)
	if !ok {
		return
	}
	if err := req.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	reqID, err := h.usecase.SaveStorage(r.Context(), userID, serverID, req)
	if err != nil {
		webutil.Error(w, "failed to save storage", nil)
		return
	}
	accepted(w, "save storage dispatched", reqID)
}

// DeleteStorage removes a storage; the agent refuses while an app uses it.
func (h *Handler) DeleteStorage(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[command.DeleteStorageRequest](w, r)
	if !ok {
		return
	}
	if err := req.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	reqID, err := h.usecase.DeleteStorage(r.Context(), userID, serverID, req.Name)
	if err != nil {
		webutil.Error(w, "failed to remove storage", nil)
		return
	}
	accepted(w, "remove storage dispatched", reqID)
}
