package bootstrap

import (
	"context"
	redisbus "winterflow/internal/infra/transport/redis/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// HubDeps holds the dependencies the gRPC Hub needs: a Bus to bridge the agent
// streams to the distributed API. The request-queue subscriber and the
// response-queue publisher are wired into the Hub itself (Phase 3); bootstrap
// only establishes the Bus connection.
type HubDeps struct {
	Log *logger.Logger
	Cfg *config.ServerConfig
	Bus *redisbus.Bus
}

// BootstrapHUB connects to Redis and returns the Hub's dependencies.
func BootstrapHUB(ctx context.Context, log *logger.Logger, cfg *config.ServerConfig) *HubDeps {
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

	return &HubDeps{
		Log: log,
		Cfg: cfg,
		Bus: redisbus.NewBus(rc, log),
	}
}
