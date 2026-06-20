package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	appagent "winterflow/internal/app/agent"
	"winterflow/internal/domain/model"
	notificationsvc "winterflow/internal/domain/service/notification"
	agentsrv "winterflow/internal/infra/agent/service"
	"winterflow/internal/infra/cert"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/repository"
	dbservice "winterflow/internal/infra/db/service"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/internal/infra/transport/mem/service/reply"
	busappsrv "winterflow/internal/infra/transport/redis/service/app"
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

	userRepo := repository.NewDbUserRepository(dbconn, log)
	serverRepo := repository.NewDbServerRepository(dbconn, log)

	// In-process bus + reply manager + response subscriber: identical wiring to
	// the distributed API, but Redis is replaced by an in-memory bus.
	b := membus.NewBus(log)
	rm := reply.NewReplyManager(log)
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
			rm.Publish(ntf.Ref, ntf)
		}
	}()

	// In-process bridge: consumes the request queue and runs commands against
	// the local Docker Compose orchestrator (the standalone Hub + agent).
	orchestrator := dockercompose.NewRepository(cfg, log)
	dispatcher := appagent.NewDispatcher(orchestrator, log)
	bridge := appagent.NewInProcessBridge(b, cfg, dispatcher, log)
	if err := bridge.Start(ctx); err != nil {
		log.Fatalf("failed to start in-process bridge: %v", err)
	}

	deps := &Deps{
		Log:                 log,
		Cfg:                 cfg,
		UserService:         dbservice.NewDbUserService(log, userRepo),
		ServerService:       dbservice.NewDbServerService(log, serverRepo),
		AppService:          busappsrv.NewAppService(log, cfg, b, rm),
		NotificationManager: notificationsvc.NewNotificationManager(),
	}

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
		code := util.GenerateRandomCode(6)
		serverID, err := agentservice.Register(context.TODO(), code)
		if err != nil {
			log.Fatalf("Failed to register server: %v", err)
		}
		log.Info("Agent registered successfully with Server ID: %s and Code: %s", serverID, code)
		defer fmt.Printf("\n\nVisit winterflow and add your server by using the code: \n     %s\n\n\n\n", code)
	}

	return deps
}
