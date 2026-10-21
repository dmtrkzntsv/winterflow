package bootstrap

import (
	"context"
	grpchub "winterflow/internal/infra/transport/grpc/hub"
	redisbus "winterflow/internal/infra/transport/redis/bus"
	hubsreg "winterflow/internal/infra/transport/redis/hubs"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// HubDeps holds the dependencies the gRPC Hub needs: a Bus to bridge the agent
// streams to the distributed API, the hub list it announces itself in (same
// Redis connection), and the authorizer that asks the API whether a
// connecting agent's certificate is still its server's active one. The request-queue subscriber, the response-queue
// publisher and the announcer are wired into the Hub itself.
type HubDeps struct {
	Log      *logger.Logger
	Cfg      *config.ServerConfig
	Bus      *redisbus.Bus
	Registry *hubsreg.Registry
	Auth     *grpchub.BusAuthorizer
}

// BootstrapHUB connects to Redis and returns the Hub's dependencies. The
// authorizer's reply subscription lives until ctx ends.
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

	b := redisbus.NewBus(rc, log)
	auth := grpchub.NewBusAuthorizer(b, cfg, cfg.GetHubID(), grpchub.AuthorizeTimeout, log)
	if err := auth.Start(ctx); err != nil {
		log.Fatalf("failed to start agent authorizer: %v", err)
	}

	return &HubDeps{
		Log:      log,
		Cfg:      cfg,
		Bus:      b,
		Registry: hubsreg.NewRegistry(rc),
		Auth:     auth,
	}
}
