package bootstrap

import (
	"context"
	redisbus "winterflow/internal/infra/transport/redis/bus"
	hubsreg "winterflow/internal/infra/transport/redis/hubs"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// HubDeps holds the dependencies the gRPC Hub needs: a Bus to bridge the agent
// streams to the distributed API, and the hub list it announces itself in
// (same Redis connection). The request-queue subscriber, the response-queue
// publisher and the announcer are wired into the Hub itself.
type HubDeps struct {
	Log      *logger.Logger
	Cfg      *config.ServerConfig
	Bus      *redisbus.Bus
	Registry *hubsreg.Registry
}

// BootstrapHUB connects to Redis and returns the Hub's dependencies.
func BootstrapHUB(ctx context.Context, log *logger.Logger, cfg *config.ServerConfig) *HubDeps {
	if err := cfg.ValidateHub(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}
	addr, _, redisDB := cfg.GetRedisCredentials()
	rc, err := redisbus.NewRedisClient(cfg)
	if err != nil {
		log.Fatalf("invalid redis configuration: %v", err)
	}
	if redisbus.Ping(ctx, rc) != nil {
		log.Fatalf("failed to connect to redis at %s", addr)
	}
	log.Debug("connected to redis", "addr", addr, "db", redisDB)

	return &HubDeps{
		Log:      log,
		Cfg:      cfg,
		Bus:      redisbus.NewBus(rc, log),
		Registry: hubsreg.NewRegistry(rc),
	}
}
