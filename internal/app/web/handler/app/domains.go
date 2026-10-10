package server

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/command"
)

// serverFromQuery resolves the caller and the server_id query parameter and
// enforces the caller's org owns that server, writing 401/400/403 itself.
func (h *Handler) serverFromQuery(w http.ResponseWriter, r *http.Request) (userID, serverID string, ok bool) {
	userID, ok = webutil.RequireUser(w, r)
	if !ok {
		return "", "", false
	}
	serverID = r.URL.Query().Get(serverIDKey)
	if serverID == "" {
		webutil.Error(w, "server_id is required", nil)
		return "", "", false
	}
	if !webutil.RequireServerAccess(w, r, h.servers, userID, serverID) {
		return "", "", false
	}
	return userID, serverID, true
}

// ListRegisteredDomains dispatches domain.list; the registrations (with the
// apps using each) arrive over SSE and refresh the server_domains index.
func (h *Handler) ListRegisteredDomains(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	reqID, err := h.usecase.ListServerDomains(r.Context(), userID, serverID)
	if err != nil {
		webutil.Error(w, "failed to list domains", nil)
		return
	}
	accepted(w, "list domains dispatched", reqID)
}

// CreateDomain registers a domain on the server, or replaces an existing
// registration (how a certificate is renewed).
func (h *Handler) CreateDomain(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[command.CreateDomainRequest](w, r)
	if !ok {
		return
	}
	if err := req.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	reqID, err := h.usecase.CreateDomain(r.Context(), userID, serverID, req)
	if err != nil {
		webutil.Error(w, "failed to register domain", nil)
		return
	}
	accepted(w, "register domain dispatched", reqID)
}

// DeleteDomain removes a registration; the agent refuses while an app uses it.
func (h *Handler) DeleteDomain(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[command.DeleteDomainRequest](w, r)
	if !ok {
		return
	}
	if err := req.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	reqID, err := h.usecase.DeleteDomain(r.Context(), userID, serverID, req.Name)
	if err != nil {
		webutil.Error(w, "failed to remove domain", nil)
		return
	}
	accepted(w, "remove domain dispatched", reqID)
}
