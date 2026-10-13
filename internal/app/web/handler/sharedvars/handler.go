// Package sharedvars serves org- and server-level shared variables. Every
// server-addressed route verifies the caller's org owns the server; every
// server id inside a body (ciphertexts keys, resync/reload targets) is
// checked against the caller's org by the usecase. No route ever returns a
// secret value or a ciphertext.
package sharedvars

import (
	"errors"
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	svuc "winterflow/internal/domain/usecase/sharedvars"
	"winterflow/pkg/logger"
)

type Deps struct {
	Logger              *logger.Logger
	Repo                port.SharedVariableRepository
	Servers             port.ServerRepository
	Users               webutil.OrgResolver
	CommandDispatcher   port.CommandDispatcher
	NotificationManager port.NotificationManager
	StatusCache         *status.Cache
	// Reloads is the rollout queue (nil: reload requests fail).
	Reloads svuc.ReloadQueue
}

type Handler struct {
	uc      *svuc.UseCase
	users   webutil.OrgResolver
	servers webutil.ServerAccess
	log     *logger.Logger
}

// NewHandler wires the usecase. With zero Deps (route documentation) it
// builds a handler that is never called.
func NewHandler(d *Deps) *Handler {
	deps := svuc.Deps{Repo: d.Repo, Dispatcher: d.CommandDispatcher, Reloads: d.Reloads, Log: d.Logger}
	var access webutil.ServerAccess
	if d.Servers != nil {
		deps.Servers = d.Servers
		access = d.Servers
	}
	if d.NotificationManager != nil {
		deps.Notifications = d.NotificationManager
	}
	if d.StatusCache != nil { // a nil *status.Cache must not become a non-nil interface
		deps.Revisions = d.StatusCache
	}
	return &Handler{uc: svuc.New(&deps), users: d.Users, servers: access, log: d.Logger}
}

// callerOrg resolves the caller and their org, writing 401/400 itself.
func (h *Handler) callerOrg(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	return webutil.CallerOrg(w, r, h.users, h.log)
}

// serverAccess requires serverID (400) owned by the caller's org (403).
func (h *Handler) serverAccess(w http.ResponseWriter, r *http.Request, userID, serverID string) bool {
	return webutil.RequireOwnedServer(w, r, h.servers, userID, serverID)
}

// orgFor is the org an optional server_id addresses: the server's (after
// the ownership check) or, without one, the caller's.
func (h *Handler) orgFor(w http.ResponseWriter, r *http.Request, userID, serverID string) (string, bool) {
	if serverID == "" {
		_, orgID, ok := h.callerOrg(w, r)
		return orgID, ok
	}
	if !webutil.RequireServerAccess(w, r, h.servers, userID, serverID) {
		return "", false
	}
	orgID, err := h.uc.ServerOrg(r.Context(), serverID)
	if err != nil {
		h.fail(w, err, "resolve the server's organization")
		return "", false
	}
	return orgID, true
}

// fail maps usecase errors: validation 400 with its message, a foreign
// server 403, a conflict (a stopping reload rollout) 409, anything else a
// logged generic 400.
func (h *Handler) fail(w http.ResponseWriter, err error, what string) {
	var ve *svuc.ValidationError
	switch {
	case errors.As(err, &ve):
		webutil.Error(w, ve.Msg, nil)
	case errors.Is(err, svuc.ErrForbidden):
		webutil.Forbidden(w)
	case errors.Is(err, model.ErrConflict):
		webutil.ErrorStatus(w, webutil.StatusFor(err), err.Error())
	default:
		h.log.Error("shared variables: "+what, "error", err)
		webutil.Error(w, "failed to "+what, nil)
	}
}

func (h *Handler) ListOrg(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	out, err := h.uc.ListOrg(r.Context(), orgID)
	if err != nil {
		h.fail(w, err, "list shared variables")
		return
	}
	webutil.Success(w, "", out)
}

func (h *Handler) EncryptionKeys(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	out, err := h.uc.EncryptionKeys(r.Context(), orgID)
	if err != nil {
		h.fail(w, err, "list encryption keys")
		return
	}
	webutil.Success(w, "", out)
}

func (h *Handler) SaveOrg(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[SaveVariableRequest](w, r)
	if !ok {
		return
	}
	out, err := h.uc.SaveOrg(r.Context(), userID, orgID, req.input())
	if err != nil {
		h.fail(w, err, "save shared variable")
		return
	}
	webutil.Success(w, "shared variable saved", out)
}

func (h *Handler) DeleteOrg(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[DeleteVariableRequest](w, r)
	if !ok {
		return
	}
	if err := h.uc.DeleteOrg(r.Context(), userID, orgID, req.ID, req.Reload); err != nil {
		h.fail(w, err, "delete shared variable")
		return
	}
	webutil.Success(w, "shared variable deleted", nil)
}

func (h *Handler) ListServer(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	serverID := r.URL.Query().Get("server_id")
	if !h.serverAccess(w, r, userID, serverID) {
		return
	}
	out, err := h.uc.ListServer(r.Context(), serverID)
	if err != nil {
		h.fail(w, err, "list shared variables")
		return
	}
	webutil.Success(w, "", out)
}

func (h *Handler) SaveServer(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[SaveServerVariableRequest](w, r)
	if !ok || !h.serverAccess(w, r, userID, req.ServerID) {
		return
	}
	out, err := h.uc.SaveServer(r.Context(), userID, req.ServerID, req.input())
	if err != nil {
		h.fail(w, err, "save shared variable")
		return
	}
	webutil.Success(w, "shared variable saved", out)
}

func (h *Handler) DeleteServer(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[DeleteServerVariableRequest](w, r)
	if !ok || !h.serverAccess(w, r, userID, req.ServerID) {
		return
	}
	if err := h.uc.DeleteServer(r.Context(), userID, req.ServerID, req.ID, req.Reload); err != nil {
		h.fail(w, err, "delete shared variable")
		return
	}
	webutil.Success(w, "shared variable deleted", nil)
}

func (h *Handler) Resync(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[ResyncRequest](w, r)
	if !ok {
		return
	}
	orgID, ok := h.orgFor(w, r, userID, req.ServerID)
	if !ok {
		return
	}
	servers, err := h.uc.Resync(r.Context(), orgID, svuc.ResyncInput{ServerID: req.ServerID, VariableID: req.VariableID})
	if err != nil {
		h.fail(w, err, "resync shared variables")
		return
	}
	webutil.Success(w, "resync dispatched", svuc.DispatchedServers{Servers: servers})
}

func (h *Handler) Reload(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[ReloadRequest](w, r)
	if !ok {
		return
	}
	orgID, ok := h.orgFor(w, r, userID, req.ServerID)
	if !ok {
		return
	}
	servers, err := h.uc.Reload(r.Context(), userID, orgID, svuc.ReloadInput{
		ServerID: req.ServerID, VariableID: req.VariableID, Names: req.Names, AppIDs: req.AppIDs,
	})
	if err != nil {
		h.fail(w, err, "reload apps")
		return
	}
	webutil.Success(w, "reload queued", svuc.DispatchedServers{Servers: servers})
}
