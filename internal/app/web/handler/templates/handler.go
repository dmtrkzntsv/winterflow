// Package templates serves templates (org-scoped) and their instances
// (server-addressed: every instance route checks the caller's org owns the
// server). Instance calls answer 202 with a request_id, or with wait:true
// hold the request for the agent's outcome, at most WaitBound.
package templates

import (
	"context"
	"errors"
	"net/http"
	"time"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	appuc "winterflow/internal/domain/usecase/app"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/pkg/logger"
)

// WaitBound is how long a wait:true call holds its request (an image pull
// can take minutes); after it the call answers 202 and the outcome is read
// with get-instance.
const WaitBound = 10 * time.Minute

// OrgResolver resolves a user's organization (port.UserService).
type OrgResolver interface {
	PrimaryOrganizationID(ctx context.Context, userID string) (string, error)
}

// Deps wires the handler; with zero Deps (route documentation) it is never
// called.
type Deps struct {
	Logger                  *logger.Logger
	Templates               port.TemplateRepository
	SharedVariables         tuc.SharedNames
	Users                   OrgResolver
	Servers                 webutil.ServerAccess
	CommandDispatcher       port.CommandDispatcher
	AppRepository           port.AppRepository
	AppDomainRepository     port.AppDomainRepository
	ServerDomainRepository  port.ServerDomainRepository
	ServerStorageRepository port.ServerStorageRepository
	StatusCache             *status.Cache
}

// Handler serves the template and instance routes.
type Handler struct {
	uc        *tuc.UseCase
	users     OrgResolver
	servers   webutil.ServerAccess
	log       *logger.Logger
	waitBound time.Duration
}

// NewHandler wires the usecases.
func NewHandler(d *Deps) *Handler {
	apps := appuc.NewUseCase(&appuc.Deps{
		CommandDispatcher:       d.CommandDispatcher,
		AppRepository:           d.AppRepository,
		AppDomainRepository:     d.AppDomainRepository,
		ServerDomainRepository:  d.ServerDomainRepository,
		ServerStorageRepository: d.ServerStorageRepository,
		Servers:                 d.Servers,
		Log:                     d.Logger,
	})
	deps := tuc.Deps{Repo: d.Templates, Apps: apps, Shared: d.SharedVariables, Log: d.Logger}
	if d.StatusCache != nil { // a nil *status.Cache must not become a non-nil interface
		deps.Status = d.StatusCache
	}
	return &Handler{uc: tuc.New(deps), users: d.Users, servers: d.Servers, log: d.Logger, waitBound: WaitBound}
}

// callerOrg resolves the caller and their org, writing 401/400 itself.
func (h *Handler) callerOrg(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return "", "", false
	}
	if h.users == nil {
		webutil.Error(w, "failed to resolve organization", nil)
		return "", "", false
	}
	orgID, err := h.users.PrimaryOrganizationID(r.Context(), userID)
	if err != nil {
		h.log.Error("resolve caller org", "error", err, "user_id", userID)
		webutil.Error(w, "failed to resolve organization", nil)
		return "", "", false
	}
	return userID, orgID, true
}

// requireID answers 400 unless id is a template id.
func requireID(w http.ResponseWriter, field, id string) bool {
	if !tuc.ValidID(id) {
		webutil.Error(w, field+" must be a template id", nil)
		return false
	}
	return true
}

// serverCaller resolves the caller and their org and requires serverID
// (400) owned by the caller's org (403).
func (h *Handler) serverCaller(w http.ResponseWriter, r *http.Request, serverID string) (string, string, bool) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return "", "", false
	}
	if serverID == "" {
		webutil.Error(w, "server_id is required", nil)
		return "", "", false
	}
	if !webutil.RequireServerAccess(w, r, h.servers, userID, serverID) {
		return "", "", false
	}
	return userID, orgID, true
}

// fail maps usecase errors: unknown (or another org's) 404, a conflict
// 409, a fixable request 400 with its message, anything else a logged
// generic 400.
func (h *Handler) fail(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, model.ErrNotFound):
		webutil.ErrorStatus(w, http.StatusNotFound, err.Error())
	case errors.Is(err, model.ErrConflict):
		webutil.ErrorStatus(w, http.StatusConflict, err.Error())
	case errors.Is(err, model.ErrRejected):
		webutil.Error(w, err.Error(), nil)
	default:
		h.log.Error("templates: "+what, "error", err)
		webutil.Error(w, "failed to "+what, nil)
	}
}
