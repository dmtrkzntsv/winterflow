package bootstrap

import (
	"context"
	"encoding/json"
	"winterflow/internal/domain/model"
	notificationsvc "winterflow/internal/domain/service/notification"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/repository"
	dbservice "winterflow/internal/infra/db/service"
	"winterflow/internal/infra/transport/dispatch"
	redisbus "winterflow/internal/infra/transport/redis/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// BootstrapAPI wires the distributed "brain": DB-backed user/server/app services
// (the API owns persistence) and a fire-and-forward command dispatcher. The
// API publishes commands to requests:<region> and drains its region's response
// queue, routing each agent result to the originating user over SSE.
func BootstrapAPI(ctx context.Context, log *logger.Logger, cfg *config.ServerConfig) *Deps {
	addr, pass, redisDB := cfg.GetRedisCredentials()
	rc := redisbus.NewClient(redisbus.Config{
		Addr:     addr,
		Password: pass,
		DB:       redisDB,
	})
	if redisbus.Ping(ctx, rc) != nil {
		log.Fatalf("failed to connect to redis at %s", addr)
	}
	log.Debug("connected to redis", "addr", addr, "db", redisDB)

	b := redisbus.NewBus(rc, log)
	nm := notificationsvc.NewNotificationManager()
	dispatcher := dispatch.NewManager(b, nm, cfg, log)
	statusCache := status.NewCache(statusTTL)

	startResponseSubscriber(ctx, b, dispatcher, cfg, log)

	dbconn := db.NewBunConnection(log, cfg.GetDbURL())
	userRepo := repository.NewDbUserRepository(dbconn, log)
	serverRepo := repository.NewDbServerRepository(dbconn, log)
	appRepo := repository.NewDbAppRepository(dbconn, log)

	startEventsSubscriber(ctx, b, statusCache, serverRepo, cfg, log)

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
	}
}

// startResponseSubscriber drains the region's response queue and hands each
// result to the dispatcher, which routes it to the originating user's SSE.
func startResponseSubscriber(ctx context.Context, b *redisbus.Bus, dispatcher *dispatch.Manager, cfg *config.ServerConfig, log *logger.Logger) {
	go func() {
		msgs, cancel, err := b.Subscribe(ctx, cfg.GetBusResponseQueue())
		if err != nil {
			log.Fatalf("failed to subscribe to response queue: %v", err)
		}
		defer cancel()
		for msg := range msgs {
			var ntf model.Notification
			if err := json.Unmarshal([]byte(msg.Payload), &ntf); err != nil {
				log.Error("failed to unmarshal bus message", err)
				continue
			}
			dispatcher.HandleResult(ntf)
		}
	}()
}
