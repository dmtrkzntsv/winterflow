package bootstrap

import (
	"context"

	notificationsvc "winterflow/internal/domain/service/notification"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/repository"
	dbservice "winterflow/internal/infra/db/service"
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/dispatch"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// wireCore builds the wiring both topologies share: DB-backed repositories and
// services, the notification manager, the command dispatcher, the status
// cache, and the response/events subscribers. The topologies differ only in
// the bus they pass (Redis vs in-process) and the extras they add afterwards
// (standalone: certs, in-process bridge, self-registration; api: nothing).
// The concrete server repository is returned alongside Deps for standalone's
// registration flow, which needs methods beyond the port.
func wireCore(ctx context.Context, b bus.Bus, dbconn *db.BunConnection, cfg *config.ServerConfig, log *logger.Logger) (*Deps, *repository.DbServerRepository) {
	userRepo := repository.NewDbUserRepository(dbconn, log)
	serverRepo := repository.NewDbServerRepository(dbconn, log)
	appRepo := repository.NewDbAppRepository(dbconn, log)

	nm := notificationsvc.NewNotificationManager()
	dispatcher := dispatch.NewManager(b, nm, cfg, log)
	statusCache := status.NewCache(statusTTL)

	startResponseSubscriber(ctx, b, dispatcher, cfg, log)
	startEventsSubscriber(ctx, b, statusCache, serverRepo, nm, cfg, log)

	return &Deps{
		Log:                 log,
		Cfg:                 cfg,
		UserService:         dbservice.NewDbUserService(log, userRepo),
		ServerService:       dbservice.NewDbServerService(log, serverRepo),
		ServerRepository:    serverRepo,
		AppRepository:       appRepo,
		CommandDispatcher:   dispatcher,
		NotificationManager: nm,
		StatusCache:         statusCache,
	}, serverRepo
}
