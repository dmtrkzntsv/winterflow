package bootstrap

import (
	"context"
	"encoding/json"
	"winterflow/internal/domain/model"
	notificationsvc "winterflow/internal/domain/service/notification"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/repository"
	dbservice "winterflow/internal/infra/db/service"
	"winterflow/internal/infra/transport/mem/service/reply"
	redisbus "winterflow/internal/infra/transport/redis/bus"
	redisappsrv "winterflow/internal/infra/transport/redis/service/app"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// BootstrapAPI wires the distributed "brain": DB-backed user/server services
// (the API still owns persistence) and a Redis-backed AppService that publishes
// commands onto the Bus. A single subscriber drains the response queue and hands
// replies to the reply.Manager so blocked callers wake up.
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
	rm := reply.NewReplyManager(log)
	go func() {
		msgs, cancel, err := b.Subscribe(ctx, cfg.GetBusResponseQueue())
		if err != nil {
			log.Fatalf("failed to listen bus: %v", err)
		}
		defer cancel()

		for msg := range msgs {
			log.Debug("received message", "channel", msg.Channel)
			ntf := model.Notification{}
			err := json.Unmarshal([]byte(msg.Payload), &ntf)
			if err != nil {
				log.Error("failed to unmarshal bus message", err)
				continue
			}
			rm.Publish(ntf.Ref, ntf)
		}
	}()

	dbconn := db.NewBunConnection(log, cfg.GetDbURL())
	userRepo := repository.NewDbUserRepository(dbconn, log)
	serverRepo := repository.NewDbServerRepository(dbconn, log)

	return &Deps{
		Log:                 log,
		Cfg:                 cfg,
		UserService:         dbservice.NewDbUserService(log, userRepo),
		ServerService:       dbservice.NewDbServerService(log, serverRepo),
		AppService:          redisappsrv.NewAppService(log, cfg, b, rm),
		NotificationManager: notificationsvc.NewNotificationManager(),
	}
}
