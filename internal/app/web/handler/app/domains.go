package server

import (
	"context"
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// serverFromQuery reads the required server_id query parameter (400 when
// missing), then authorizes the caller for it (401/403), writing the error
// itself.
func (h *Handler) serverFromQuery(w http.ResponseWriter, r *http.Request) (userID, serverID string, ok bool) {
	serverID = r.URL.Query().Get(serverIDKey)
	if serverID == "" {
		webutil.Error(w, "server_id is required", nil)
		return "", "", false
	}
	userID, ok = h.authorize(w, r, serverID)
	return userID, serverID, ok
}

// dispatchBody serves a server-addressed command with a JSON body: it
// authorizes the caller for server_id, decodes and validates the body
// (400 with the reason), dispatches it, and answers 202 with the request id.
// what names the command in the answers ("add domain").
func dispatchBody[T interface{ Validate() error }](h *Handler, w http.ResponseWriter, r *http.Request, what string,
	dispatch func(ctx context.Context, userID, serverID string, req T) (string, error)) {
	userID, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[T](w, r)
	if !ok {
		return
	}
	if err := req.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	reqID, err := dispatch(r.Context(), userID, serverID, req)
	if err != nil {
		webutil.Error(w, "failed to "+what, nil)
		return
	}
	accepted(w, what+" dispatched", reqID)
}

// GetDomainsResponse is the data of GET /api/v1/domain/get-domains.
type GetDomainsResponse struct {
	Domains []model.RegisteredDomain `json:"domains"`
}

// GetDomains answers the server's registrations from the API's index
// (synchronous, no agent round-trip). The index is filled when the agent
// comes online and kept current by domain.create/delete and domain.list.
func (h *Handler) GetDomains(w http.ResponseWriter, r *http.Request) {
	_, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	doms, err := h.usecase.ServerDomains(r.Context(), serverID)
	if err != nil {
		webutil.Error(w, "failed to load domains", nil)
		return
	}
	webutil.Success(w, "", GetDomainsResponse{Domains: doms})
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
	dispatchBody(h, w, r, "add domain", h.usecase.CreateDomain)
}

// DeleteDomain removes a registration; the agent refuses while an app uses it.
func (h *Handler) DeleteDomain(w http.ResponseWriter, r *http.Request) {
	dispatchBody(h, w, r, "remove domain", func(ctx context.Context, userID, serverID string, req command.DeleteDomainRequest) (string, error) {
		return h.usecase.DeleteDomain(ctx, userID, serverID, req.Name)
	})
}
