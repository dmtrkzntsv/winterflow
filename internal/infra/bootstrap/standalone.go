package bootstrap

import (
	"context"
	"time"
	appagent "winterflow/internal/app/agent"
	"winterflow/internal/domain/service/status"
	agentsrv "winterflow/internal/infra/agent/service"
	"winterflow/internal/infra/cert"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/repository"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

// BootstrapStandalone wires the single-process topology: SQLite-backed services
// plus a local certificate manager. On first run it generates the server
// certificate and self-registers the embedded agent, printing the pairing code.
//
// The app pipeline runs entirely in-process: an in-memory bus carries the same
// command/response messages the distributed topology uses, an in-process bridge
// replaces the gRPC Hub, and the Docker Compose orchestrator executes commands
// locally. This proves the layered design supports the single-server app.
func BootstrapStandalone(ctx context.Context, log *logger.Logger, cfg *config.ServerConfig) *Deps {
	dbconn := db.NewBunConnection(log, cfg.GetDbURL())

	certmanager, err := cert.NewManager(cfg, log)
	if err != nil {
		log.Fatalf("Failed to create certificate manager: %v", err)
	}

	// Identical core wiring to the distributed API; Redis is replaced by an
	// in-memory bus.
	b := membus.NewBus(log)
	deps, serverRepo := wireCore(ctx, b, dbconn, cfg, log)

	// In-process bridge: consumes the request queue and runs commands against
	// the local Docker Compose orchestrator (the standalone Hub + agent).
	orchestrator := dockercompose.NewRepository(cfg, log)
	agentDispatcher := appagent.NewDispatcher(orchestrator, log)
	bridge := appagent.NewInProcessBridge(b, cfg, agentDispatcher, log)
	if err := bridge.Start(ctx); err != nil {
		log.Fatalf("failed to start in-process bridge: %v", err)
	}

	// Standalone has no gRPC Hub emitting heartbeats; the embedded agent is the
	// box itself and is "online" while the process runs. Keep the status cache
	// warm with a periodic liveness pulse for the local server.
	go markEmbeddedServerOnline(ctx, serverRepo, deps.StatusCache, log)

	if !cert.IsServerCertificateGenerated(certmanager) {
		certmanager.GenerateServer(true)
	}

	// Standalone keeps exactly one claimable registration for its embedded
	// agent until a server is claimed. Gating on "is a server claimed?" (rather
	// than "do certs exist?") means an expired or leftover registration is
	// replaced with a fresh one on boot, so the box stays claimable. Once
	// claimed, the first login's auto-claim materialized the server and this is
	// skipped.
	agentservice := agentsrv.NewAgentService(log, certmanager, serverRepo)
	claimed, err := serverRepo.HasAnyServer(context.TODO())
	if err != nil {
		log.Fatalf("Failed to check server registration state: %v", err)
	}
	if !claimed {
		if err := serverRepo.ClearPendingRegistrations(context.TODO()); err != nil {
			log.Fatalf("Failed to clear stale registrations: %v", err)
		}
		// The code is an internal detail in standalone: the embedded server is
		// claimed automatically on first login, so there is nothing for the
		// user to "visit and enter". Keep registration silent (debug only) — no
		// pairing instructions in the CLI output.
		code := util.GenerateRandomCode(6)
		serverID, err := agentservice.Register(context.TODO(), code)
		if err != nil {
			log.Fatalf("Failed to register server: %v", err)
		}
		log.Debug("standalone server registered", "server_id", serverID)
	}

	return deps
}

// markEmbeddedServerOnline keeps the local server's liveness fresh in the status
// cache. The standalone process is the agent, so it is online while running.
func markEmbeddedServerOnline(ctx context.Context, serverRepo *repository.DbServerRepository, cache *status.Cache, log *logger.Logger) {
	pulse := func() {
		id, ok, err := serverRepo.FirstServerID(ctx)
		if err != nil {
			log.Debug("liveness pulse: lookup failed", "error", err)
			return
		}
		if ok {
			cache.MarkOnline(id, time.Now())
		}
	}
	pulse()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pulse()
		}
	}
}
