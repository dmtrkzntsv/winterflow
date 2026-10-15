package bootstrap

import (
	"context"
	"encoding/json"
	"time"
	appagent "winterflow/internal/app/agent"
	"winterflow/internal/app/agent/backup"
	agentsrv "winterflow/internal/infra/agent/service"
	"winterflow/internal/infra/cert"
	"winterflow/internal/infra/db"
	serverrepo "winterflow/internal/infra/db/repository/server"
	ingresscaddy "winterflow/internal/infra/ingress/caddy"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/internal/infra/transport/bus"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/crypto"
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
	dbconn, err := db.Open(log, cfg.GetDbURL())
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	certmanager, err := cert.NewManager(cfg, log)
	if err != nil {
		log.Fatalf("Failed to create certificate manager: %v", err)
	}

	// Identical core wiring to the distributed API; Redis is replaced by an
	// in-memory bus.
	b := membus.NewBus(log)
	deps, serverRepo := WireCore(ctx, b, dbconn, cfg, log)

	// In-process bridge: consumes the request queue and runs commands against
	// the local Docker Compose orchestrator (the standalone Hub + agent). The
	// ingress manager owns the embedded reverse proxy; a start failure
	// disables ingress but never the server.
	orchestrator := dockercompose.NewRepository(cfg, log)
	ingressManager := ingresscaddy.NewManager(cfg, log)
	if err := ingressManager.Start(ctx); err != nil {
		log.Warn("ingress manager start", "error", err)
	}
	// One sink for every embedded-agent event (status, reload outcomes):
	// straight onto the in-process events queue. Until the embedded server
	// is claimed there is no server id to report as, so events are skipped.
	sink := func(kind bus.EventKind, payload []byte) error {
		id, ok, err := serverRepo.FirstServerID(ctx)
		if err != nil || !ok {
			return err
		}
		return b.Publish(ctx, cfg.GetBusEventsQueue(), bus.EventMessage{ServerID: id, Kind: kind, Payload: payload})
	}
	// Shared-variable reloads run on their own goroutine: the bridge drains
	// every command on one goroutine, so vars.reload only queues work.
	reloads := appagent.NewReloadWorker(orchestrator, sink, log)
	go reloads.Run(ctx)
	agentDispatcher := appagent.NewDispatcher(orchestrator, ingressManager, log).WithReloads(reloads)
	// Backups run on their own goroutine, never on the bridge's.
	backups := backup.New(orchestrator, cfg.GetAgentDataDir(), log)
	agentDispatcher.SetBackups(backups)
	go appagent.RunBackups(ctx, orchestrator, backups, log)
	bridge := appagent.NewInProcessBridge(b, cfg, agentDispatcher, log)
	if err := bridge.Start(ctx); err != nil {
		log.Fatalf("failed to start in-process bridge: %v", err)
	}
	// Only now can a dispatched command be consumed (membus drops messages
	// without a subscriber), so the rollout worker starts after the bridge.
	StartRollouts(ctx, deps)

	// Report the embedded server's capabilities (specs, IP, version, public
	// key) through the same events path a distributed agent's registration
	// uses. Retries until the server has been claimed (no id to attach them to
	// before that), then exits.
	go publishEmbeddedCapabilities(ctx, b, serverRepo, cfg, log, ingressManager.Enabled())

	// Standalone has no gRPC Hub forwarding agent events; the embedded agent is
	// the box itself. Run the same status reporter the distributed agent runs,
	// publishing apps.status straight onto the in-process events queue — that
	// feeds the status cache (doubling as the liveness pulse) and the SSE push.
	// The snapshot carries each app's backup health too and lets the metrics
	// reporter reuse the status reporter's `docker ps` instead of running its
	// own.
	statusSnapshot := appagent.NewStatusSnapshot(appagent.WithBackupHealth(orchestrator, backups))
	go appagent.RunStatusReporter(ctx, statusSnapshot, sink, 30*time.Second, backups.Changed(), log)

	// Resource metrics (server.metrics every 60 s, disk scan hourly) through
	// the same events path; off with METRICS_ENABLED=false.
	appagent.StartMetrics(ctx, cfg, statusSnapshot, orchestrator, sink, log)

	// Auto-update for git-sourced apps.
	go appagent.RunSourcePoller(ctx, orchestrator, appagent.AfterSourceUpdate(ctx, backups.Reload, ingressManager, log), log)

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

// publishEmbeddedCapabilities reports the standalone box's capabilities on the
// events queue — the same EventCapabilities a distributed agent's registration
// produces — so the DB and the UI see specs/IP/version without a hub. The
// embedded server may not be claimed yet at boot; poll until it exists.
func publishEmbeddedCapabilities(ctx context.Context, b bus.Bus, serverRepo *serverrepo.Repo, cfg *config.ServerConfig, log *logger.Logger, ingressEnabled bool) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		id, ok, err := serverRepo.FirstServerID(ctx)
		if err == nil && ok {
			caps := appagent.HostCapabilities(cfg.GetAgentDataDir())
			if point, err := crypto.PublicKeyPointFromCertPath(cfg.GetAgentCertPath()); err == nil {
				caps["public_key"] = point
			}
			features := map[string]bool{
				"can_install":    true,
				"can_execute":    true,
				"can_fetch_logs": true,
				"can_monitor":    true,
				"ingress":        ingressEnabled,
			}
			body, err := json.Marshal(capabilitiesEvent{Capabilities: caps, Features: features})
			if err != nil {
				log.Error("marshal embedded capabilities", err)
				return
			}
			if err := b.Publish(ctx, cfg.GetBusEventsQueue(), bus.EventMessage{ServerID: id, Kind: bus.EventCapabilities, Payload: body}); err != nil {
				log.Error("publish embedded capabilities", err)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
