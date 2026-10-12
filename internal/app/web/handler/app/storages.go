package server

import (
	"context"
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
	dispatchBody(h, w, r, "test storage", h.usecase.TestStorage)
}

// SaveStorage adds (create) or edits a storage; the agent refuses an add
// over an existing name, an edit of a missing one or of its type, tests it,
// and redeploys the running apps that reference it.
func (h *Handler) SaveStorage(w http.ResponseWriter, r *http.Request) {
	dispatchBody(h, w, r, "save storage", h.usecase.SaveStorage)
}

// DeleteStorage removes a storage; the agent refuses while an app uses it.
func (h *Handler) DeleteStorage(w http.ResponseWriter, r *http.Request) {
	dispatchBody(h, w, r, "remove storage", func(ctx context.Context, userID, serverID string, req command.DeleteStorageRequest) (string, error) {
		return h.usecase.DeleteStorage(ctx, userID, serverID, req.Name)
	})
}
