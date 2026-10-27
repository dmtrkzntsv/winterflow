package bootstrap

import (
	"context"
	"winterflow/internal/domain/usecase/oauthlogin"

	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/internal/domain/usecase/rollouts"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/pkg/logger"
)

// StartRollouts starts the rollout worker: one per API process (standalone
// included); an idle one costs one indexed query a minute. Call it only once
// commands can flow (standalone: after the in-process bridge subscribed; API:
// after the response subscriber WireCore starts), because the first pass may
// resume an expired-lease rollout and dispatch immediately.
func StartRollouts(ctx context.Context, d *Deps) {
	go d.Rollouts.Run(ctx)
}

// CoreOption adjusts WireCore (tests).
type CoreOption func(*coreOptions)

type coreOptions struct {
	rolloutTiming rollouts.Timing
	hubRegistry   port.HubRegistry
	// signInSet replaces the configured sign-in providers
	// (WithSignInProviders).
	signInSet       bool
	signInOrigin    string
	signInProviders []oauthlogin.Provider
}

// WithHubRegistry wires the distributed API's hubs: commands are routed to
// the request queue of each server's hub, and Deps.HubPicker assigns hubs
// from reg. Without it (standalone) every command goes to the plain request
// queue the in-process bridge drains.
func WithHubRegistry(reg port.HubRegistry) CoreOption {
	return func(o *coreOptions) { o.hubRegistry = reg }
}

// WithRolloutTiming sets the rollout worker's bounds (zero fields keep
// their defaults): the contract suite's fake agent reports every 50 ms, so
// a step's two healthy reports need not be 20 s apart.
func WithRolloutTiming(t rollouts.Timing) CoreOption {
	return func(o *coreOptions) { o.rolloutTiming = t }
}

// newInstances builds the templates usecase the instance routes and the
// rollout queue's template_upgrade steps share.
func newInstances(r repos, d port.CommandDispatcher, cache *status.Cache, log *logger.Logger) *templates.UseCase {
	apps := appuc.NewUseCase(&appuc.Deps{
		CommandDispatcher:       d,
		AppRepository:           r.app,
		AppDomainRepository:     r.appDomain,
		ServerDomainRepository:  r.serverDomain,
		ServerStorageRepository: r.serverStorage,
		Servers:                 r.server,
		Log:                     log,
	})
	return templates.New(templates.Deps{Repo: r.template, Apps: apps, Shared: r.sharedVar, Status: cache, Log: log})
}

// newRollouts builds the rollout queue. Its template_upgrade steps render
// and dispatch through instances; its vars_reload steps dispatch
// vars.reload. The caller starts its worker (Run).
func newRollouts(r repos, instances *templates.UseCase, d port.CommandDispatcher, cache *status.Cache, nm port.NotificationManager, t rollouts.Timing, log *logger.Logger) *rollouts.UseCase {
	return rollouts.New(rollouts.Deps{
		Repo: r.rollout, Templates: r.template, Instances: instances, Dispatcher: d, Status: cache,
		Apps: r.app, Members: r.server, Notifications: nm, Log: log, Timing: t,
	})
}
