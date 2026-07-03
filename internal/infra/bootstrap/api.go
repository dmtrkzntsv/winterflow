package bootstrap

import (
	"context"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/transport/bus"
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
	dbconn := db.NewBunConnection(log, cfg.GetDbURL())

	deps, _ := wireCore(ctx, b, dbconn, cfg, log)
	return deps
}

// startResponseSubscriber drains the region's response queue and hands each
// result to the dispatcher, which routes it to the originating user's SSE.
// Bus-agnostic: both topologies use it (Redis in distributed, mem in
// standalone).
func startResponseSubscriber(ctx context.Context, b bus.Bus, dispatcher *dispatch.Manager, cfg *config.ServerConfig, log *logger.Logger) {
	bus.SubscribeJSON(ctx, b, cfg.GetBusResponseQueue(), log, func(ntf model.Notification) {
		dispatcher.HandleResult(ntf)
	})
}
