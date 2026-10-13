// Package rollouts serves the rollout queue: the org's rollouts and Stop.
// Every route is scoped to the caller's org: another org's rollout,
// template or org answers 404.
package rollouts

import (
	"context"
	"net/http"

	"winterflow/internal/app/web/api"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

// Queue is the rollout queue as the routes use it (*rollouts.UseCase).
type Queue interface {
	List(ctx context.Context, orgID, kind, scopeKey string) ([]model.RolloutView, error)
	Stop(ctx context.Context, orgID, id string) (model.RolloutView, error)
}

// Deps wires the handler; with zero Deps (route documentation) it is never
// called.
type Deps struct {
	Logger   *logger.Logger
	Users    webutil.OrgResolver
	Rollouts Queue
}

// Handler serves the rollout routes.
type Handler struct {
	queue Queue
	users webutil.OrgResolver
	log   *logger.Logger
}

// NewHandler builds the handler.
func NewHandler(d *Deps) *Handler {
	return &Handler{queue: d.Rollouts, users: d.Users, log: d.Logger}
}

// RolloutsQuery is the query of GET get-rollouts.
type RolloutsQuery struct {
	Kind     string `query:"kind" required:"true" description:"template_upgrade or vars_reload."`
	ScopeKey string `query:"scope_key" description:"A template id (template_upgrade) or the org id (vars_reload); absent: every scope of the org."`
}

// RolloutIDRequest is the body of POST stop-rollout.
type RolloutIDRequest struct {
	ID string `json:"id" required:"true"`
}

// RolloutList is the data of GET get-rollouts.
type RolloutList struct {
	Rollouts []model.RolloutView `json:"rollouts" description:"The active rollout first, then the last 20 ended ones of each scope, newest first."`
}

// Routes is the rollouts slice of the route table.
func (h *Handler) Routes(api.Guards) []api.Route {
	errs := []int{http.StatusBadRequest, http.StatusNotFound}
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/rollout/get-rollouts", Handler: h.GetRollouts, Auth: api.User,
			Summary: "The org's rollouts of a kind: the active one and the last 20 ended", Tag: "rollouts",
			Description: "Progress arrives as rollout events over SSE.",
			Query:       RolloutsQuery{}, Response: RolloutList{}, Errors: errs,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/rollout/stop-rollout", Handler: h.StopRollout, Auth: api.User,
			Summary: "Stop a rollout after the app in flight", Tag: "rollouts",
			Description: "An ended rollout answers as it is.",
			Request:     RolloutIDRequest{}, Response: model.RolloutView{}, Errors: errs,
		},
	}
}

func (h *Handler) GetRollouts(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := webutil.CallerOrg(w, r, h.users, h.log)
	if !ok {
		return
	}
	q := r.URL.Query()
	kind, scopeKey := q.Get("kind"), q.Get("scope_key")
	if kind == "" {
		webutil.Error(w, "kind is required", nil)
		return
	}
	if scopeKey != "" && !webutil.RequireUUID(w, scopeKey, "scope_key must be a template or org id") {
		return
	}
	list, err := h.queue.List(r.Context(), orgID, kind, scopeKey)
	if err != nil {
		h.fail(w, err, "list rollouts")
		return
	}
	webutil.Success(w, "", RolloutList{Rollouts: list})
}

func (h *Handler) StopRollout(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := webutil.CallerOrg(w, r, h.users, h.log)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[RolloutIDRequest](w, r)
	if !ok || !webutil.RequireUUID(w, req.ID, "id must be a rollout id") {
		return
	}
	v, err := h.queue.Stop(r.Context(), orgID, req.ID)
	if err != nil {
		h.fail(w, err, "stop the rollout")
		return
	}
	webutil.Success(w, "stop requested", v)
}

// fail maps usecase errors: unknown (or another org's) 404, a fixable
// request 400 with its message, anything else a logged generic 400.
func (h *Handler) fail(w http.ResponseWriter, err error, what string) {
	webutil.Fail(w, h.log, "rollouts", err, what)
}
