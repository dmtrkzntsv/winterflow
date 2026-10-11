package bootstrap

import (
	"context"

	"winterflow/internal/domain/port"
	notificationsvc "winterflow/internal/domain/service/notification"
	"winterflow/internal/domain/service/status"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/repository"
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/dispatch"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// WireCore builds the wiring both topologies share: DB-backed repositories and
// services, the notification manager, the command dispatcher, the status
// cache, and the response/events subscribers. The topologies differ only in
// the bus they pass (Redis vs in-process) and the extras they add afterwards
// (standalone: certs, in-process bridge, self-registration; api: nothing).
// The concrete server repository is returned alongside Deps for standalone's
// registration flow, which needs methods beyond the port.
func WireCore(ctx context.Context, b bus.Bus, dbconn *db.BunConnection, cfg *config.ServerConfig, log *logger.Logger) (*Deps, *repository.DbServerRepository) {
	userRepo := repository.NewDbUserRepository(dbconn, log)
	serverRepo := repository.NewDbServerRepository(dbconn, log)
	appRepo := repository.NewDbAppRepository(dbconn, log)
	appDomainRepo := repository.NewDbAppDomainRepository(dbconn, log)
	serverDomainRepo := repository.NewDbServerDomainRepository(dbconn, log)

	nm := notificationsvc.NewNotificationManager(log)
	dispatcher := dispatch.NewManager(b, nm, cfg, log)
	statusCache := status.NewCache(statusTTL)

	startResponseSubscriber(ctx, b, dispatcher, cfg, log)
	startEventsSubscriber(ctx, b, statusCache, serverRepo, nm, cfg, log, syncServerDomains(dispatcher, serverDomainRepo, log))

	return &Deps{
		Log:                    log,
		Cfg:                    cfg,
		UserService:            userRepo,
		ServerRepository:       serverRepo,
		AppRepository:          appRepo,
		AppDomainRepository:    appDomainRepo,
		ServerDomainRepository: serverDomainRepo,
		CommandDispatcher:      dispatcher,
		NotificationManager:    nm,
		StatusCache:            statusCache,
	}, serverRepo
}

// syncServerDomains fills a server's server_domains index when its agent
// comes online: a domain.list dispatched for no user (the result reaches no
// SSE stream) whose result replaces the index rows, unchanged rows costing
// no write. An agent without domain commands answers an error, which is
// ignored.
func syncServerDomains(d port.CommandDispatcher, repo port.ServerDomainRepository, log *logger.Logger) func(context.Context, string) {
	uc := appuc.NewUseCase(&appuc.Deps{CommandDispatcher: d, ServerDomainRepository: repo, Log: log})
	return func(ctx context.Context, serverID string) {
		if _, err := uc.ListServerDomains(ctx, "", serverID); err != nil {
			log.Warn("sync registered domains: dispatch domain.list", "error", err, "server_id", serverID)
		}
	}
}
