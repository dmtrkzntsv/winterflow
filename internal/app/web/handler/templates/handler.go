// Package templates serves templates (org-scoped) and their instances
// (server-addressed: every instance route checks the caller's org owns the
// server). Instance calls answer 202 with a request_id, or with wait:true
// hold the request for the agent's outcome, at most WaitBound.
package templates

import (
	"context"
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

// InstanceServers authorizes the instance routes (port.ServerRepository):
// the server must be one of the caller's organizations' and, beyond that,
// the organization the call acts for (the caller's primary one), so an
// instance never sits on another organization's server.
type InstanceServers interface {
	webutil.ServerAccess
	ServerInOrg(ctx context.Context, serverID, orgID string) (bool, error)
}

// RolloutQueue queues "Upgrade all" (*rollouts.UseCase).
type RolloutQueue interface {
	QueueUpgrade(ctx context.Context, userID, orgID, templateID string, revision int64) (model.RolloutView, error)
}

// Deps wires the handler; with zero Deps (route documentation) it is never
// called.
type Deps struct {
	Logger *logger.Logger
	// LogoClient is optional; injecting a client allows local image tests.
	LogoClient              *http.Client
	Templates               port.TemplateRepository
	SharedVariables         tuc.SharedNames
	Users                   webutil.OrgResolver
	Servers                 InstanceServers
	CommandDispatcher       port.CommandDispatcher
	AppRepository           port.AppRepository
	AppDomainRepository     port.AppDomainRepository
	ServerDomainRepository  port.ServerDomainRepository
	ServerStorageRepository port.ServerStorageRepository
	StatusCache             *status.Cache
	Rollouts                RolloutQueue
	// Instances is the shared templates usecase (bootstrap.Deps.Instances);
	// nil builds one from the fields above.
	Instances *tuc.UseCase
}

// Handler serves the template and instance routes.
type Handler struct {
	uc         *tuc.UseCase
	rollouts   RolloutQueue
	users      webutil.OrgResolver
	servers    InstanceServers
	log        *logger.Logger
	logoClient *http.Client
	templates  port.TemplateRepository
	waitBound  time.Duration
}

// NewHandler wires the usecases.
func NewHandler(d *Deps) *Handler {
	uc := d.Instances
	if uc == nil {
		uc = newUseCase(d)
	}
	return &Handler{uc: uc, rollouts: d.Rollouts, users: d.Users, servers: d.Servers, log: d.Logger, waitBound: WaitBound, logoClient: d.LogoClient, templates: d.Templates}
}

// newUseCase builds a templates usecase from d's repositories.
func newUseCase(d *Deps) *tuc.UseCase {
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
	return tuc.New(deps)
}

// callerOrg resolves the caller and their org, writing 401/400 itself.
func (h *Handler) callerOrg(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	return webutil.CallerOrg(w, r, h.users, h.log)
}

// requireID answers 400 unless id is a template id.
func requireID(w http.ResponseWriter, field, id string) bool {
	return webutil.RequireUUID(w, id, field+" must be a template id")
}

// serverCaller resolves the caller and their org and requires serverID
// (400) owned by the caller's organizations and in the org the call acts
// for (403 either way; a lookup error denies too).
func (h *Handler) serverCaller(w http.ResponseWriter, r *http.Request, serverID string) (string, string, bool) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok || !webutil.RequireOwnedServer(w, r, h.servers, userID, serverID) {
		return "", "", false
	}
	if in, err := h.servers.ServerInOrg(r.Context(), serverID, orgID); err != nil || !in {
		webutil.Forbidden(w)
		return "", "", false
	}
	return userID, orgID, true
}

// instanceCaller is serverCaller plus a required key (400).
func (h *Handler) instanceCaller(w http.ResponseWriter, r *http.Request, serverID, key string) (string, string, bool) {
	userID, orgID, ok := h.serverCaller(w, r, serverID)
	if !ok {
		return "", "", false
	}
	if key == "" {
		webutil.Error(w, "key is required", nil)
		return "", "", false
	}
	return userID, orgID, true
}

// fail maps usecase errors: unknown (or another org's) 404, a conflict
// 409, a fixable request 400 with its message, anything else a logged
// generic 400.
func (h *Handler) fail(w http.ResponseWriter, err error, what string) {
	webutil.Fail(w, h.log, "templates", err, what)
}
