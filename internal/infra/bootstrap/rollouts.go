package bootstrap

import (
	"context"

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

// newRollouts builds the rollout queue. Its template_upgrade steps render
// and dispatch through a templates usecase of their own, built exactly as
// the instance routes build theirs; its vars_reload steps dispatch
// vars.reload. The caller starts its worker (Run).
func newRollouts(r repos, d port.CommandDispatcher, cache *status.Cache, nm port.NotificationManager, log *logger.Logger) *rollouts.UseCase {
	apps := appuc.NewUseCase(&appuc.Deps{
		CommandDispatcher:       d,
		AppRepository:           r.app,
		AppDomainRepository:     r.appDomain,
		ServerDomainRepository:  r.serverDomain,
		ServerStorageRepository: r.serverStorage,
		Servers:                 r.server,
		Log:                     log,
	})
	instances := templates.New(templates.Deps{Repo: r.template, Apps: apps, Shared: r.sharedVar, Status: cache, Log: log})
	return rollouts.New(rollouts.Deps{
		Repo: r.rollout, Templates: r.template, Instances: instances, Dispatcher: d, Status: cache,
		Apps: r.app, Members: r.user, Notifications: nm, Log: log,
	})
}
