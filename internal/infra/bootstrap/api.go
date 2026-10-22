package bootstrap

import (
	"context"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/agenthub"
	"winterflow/internal/domain/usecase/enrollment"
	"winterflow/internal/domain/usecase/hubjoin"
	"winterflow/internal/infra/cert"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/dispatch"
	redisbus "winterflow/internal/infra/transport/redis/bus"
	hubsreg "winterflow/internal/infra/transport/redis/hubs"
	"winterflow/internal/infra/transport/redis/hubusers"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// BootstrapAPI wires the distributed "brain": DB-backed user/server/app services
// (the API owns persistence) and a fire-and-forward command dispatcher. The
// API publishes each command to its server's hub queue (bus:requests:<hub_id>)
// and drains the response queue, routing each agent result to the
// originating user over SSE.
func BootstrapAPI(ctx context.Context, log *logger.Logger, cfg *config.ServerConfig) *Deps {
	if err := cfg.ValidateDatabaseURL(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}
	if err := cfg.ValidateEnrollment(); err != nil {
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
	dbconn, err := db.Open(log, cfg.GetDbURL())
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	// Commands go to each server's hub's queue; the picker reads the hub
	// list the hubs announce on the same Redis connection.
	registry := hubsreg.NewRegistry(rc)
	deps, servers := WireCore(ctx, b, dbconn, cfg, log, WithHubRegistry(registry))
	// WireCore has started the response subscriber, so replies reach the
	// waiting steps; the Hub consumes the request queue independently.
	// Agents enroll by code: the API signs their certificates with the CA
	// ValidateEnrollment checked above.
	signer, err := cert.NewCASigner(cfg.GetHubCACertPath(), cfg.GetHubCAKeyPath())
	if err != nil {
		log.Fatalf("load the CA: %v", err)
	}
	deps.Enrollment = enrollment.New(deps.EnrollmentRepository, signer, deps.HubPicker, deps.HubCertRepository, nil)
	// Hubs join with a token; each gets its own Redis user, managed over
	// the API's own (admin) connection.
	users := hubusers.New(rc)
	checkHubUsersAdmin(ctx, log, users)
	deps.HubJoin = hubjoin.New(deps.HubTokenRepository, deps.HubCertRepository, users, signer, registry, nil)
	deps.AgentHub = agenthub.New(servers, registry, deps.HubPicker, deps.HubCertRepository, log)
	StartRollouts(ctx, deps)
	StartCatalog(ctx, deps)
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

// hubUsersChecker is hubusers.Users' startup check.
type hubUsersChecker interface {
	Check(ctx context.Context) error
}

// checkHubUsersAdmin logs an error when the API's Redis connection cannot
// manage the hubs' users (a REDIS_USERNAME that is not the admin, a server
// without an aclfile): every hub join would fail after spending its token.
// Not fatal: the API serves everything else.
func checkHubUsersAdmin(ctx context.Context, log *logger.Logger, users hubUsersChecker) {
	if err := users.Check(ctx); err != nil {
		log.Error("the API's Redis connection cannot manage the hubs' Redis users; hub joins will fail until REDIS_USERNAME/REDIS_PASSWORD name the admin user and the server has an aclfile", "error", err)
	}
}
