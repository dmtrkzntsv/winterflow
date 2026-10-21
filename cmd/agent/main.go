package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
	appagent "winterflow/internal/app/agent"
	"winterflow/internal/app/agent/backup"
	"winterflow/internal/app/agent/enroll"
	backupcli "winterflow/internal/app/backup/cli"
	ingresscaddy "winterflow/internal/infra/ingress/caddy"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/internal/infra/sysmetrics"
	"winterflow/internal/infra/transport/bus"
	grpcagent "winterflow/internal/infra/transport/grpc/agent"
	"winterflow/internal/infra/transport/grpc/proto"
	"winterflow/pkg/config"
	"winterflow/pkg/crypto"
	"winterflow/pkg/logger"
	"winterflow/pkg/version"

	"github.com/joho/godotenv"
)

func main() {
	// Hidden: what the agent runs inside a backup helper container (the
	// binary is mounted there at /wf), before anything reads config or logs.
	if len(os.Args) > 1 && os.Args[1] == "backup" {
		os.Exit(backupcli.Main(os.Args[2:], os.Stdout, os.Stderr))
	}
	// Hidden: first-run enrollment (shows a code, stores the signed
	// certificate). Runs before config load: no cert exists yet.
	if len(os.Args) > 1 && os.Args[1] == "enroll" {
		os.Exit(enroll.Main(os.Args[2:], os.Stdout, os.Stderr))
	}
	err := godotenv.Load()
	cfg := config.NewServerConfig("distributed")
	log := logger.NewLogger(logger.LoggerConfiguration{
		LogLevel: os.Getenv("LOG_LEVEL"),
		Service:  "winterflow-agent",
	})
	if err != nil {
		log.Info(".env not found, using system environment variables")
	}
	log.Info("Service starting", "pid", os.Getpid())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Stable identity: AGENT_ID env > agent cert CommonName (the claimed
	// server id) > an id persisted under the data dir. Never per-run.
	agentID := appagent.ResolveAgentID(cfg, log)

	agent := grpcagent.NewAgent(log, cfg, agentID)

	// Set agent capabilities and features: platform identity + hardware specs
	// + outbound IP (shared collector), plus the runtime version.
	capabilities := appagent.HostCapabilities(cfg.GetAgentDataDir())
	capabilities["go_version"] = runtime.Version()

	// Publish the agent's EC public key so the API can hand it to the browser
	// for ECIES-encrypting app secrets (the agent decrypts with its private key).
	if point, err := crypto.PublicKeyPointFromCertPath(cfg.GetAgentCertPath()); err == nil {
		capabilities["public_key"] = point
	} else {
		log.Warn("failed to export agent public key", "error", err)
	}

	// Wire the command dispatcher: incoming hub commands are executed against
	// the Docker Compose orchestrator. The ingress manager owns the embedded
	// reverse proxy; a start failure disables ingress but never the agent.
	orchestrator := dockercompose.NewRepository(cfg, log)
	ingressManager := ingresscaddy.NewManager(cfg, log)
	if err := ingressManager.Start(ctx); err != nil {
		log.Warn("ingress manager start", "error", err)
	}

	features := map[string]bool{
		"can_install":    true,
		"can_execute":    true,
		"can_fetch_logs": true,
		"can_monitor":    true,
		"ingress":        ingressManager.Enabled(),
	}

	apps := []*proto.App{
		{
			AppId:           "winterflow-agent",
			Name:            "Winterflow Agent",
			Version:         version.GetVersion(),
			ProtocolVersion: "1.0.0",
		},
	}

	agent.SetCapabilities(capabilities)
	agent.SetFeatures(features)
	agent.SetApps(apps)

	// One sink for every agent-initiated event (status, reload outcomes).
	sink := func(kind bus.EventKind, payload []byte) error {
		return agent.SendEvent(string(kind), payload)
	}

	// Shared-variable reloads run on their own goroutine, never inside a
	// command handler.
	reloads := appagent.NewReloadWorker(orchestrator, sink, log)
	go reloads.Run(ctx)

	dispatcher := appagent.NewDispatcher(orchestrator, ingressManager, log).WithReloads(reloads)
	// Backups run on their own goroutine, next to the source poller.
	backups := backup.New(orchestrator, cfg.GetAgentDataDir(), log)
	dispatcher.SetBackups(backups)
	// The snapshot carries each app's backup health too and lets the metrics
	// reporter reuse the status reporter's `docker ps`.
	statusSnapshot := appagent.NewStatusSnapshot(appagent.WithBackupHealth(orchestrator, backups))
	go appagent.RunBackups(ctx, orchestrator, backups, log)
	// The processes tab reads /proc on request; the snapshot names each
	// container's app.
	dispatcher.SetProcesses(appagent.NewProcessLister(sysmetrics.NewProcesses("/", runtime.NumCPU()), statusSnapshot))
	agent.SetDispatcher(dispatcher)

	// Supervise the connection: connect, register, stream, and reconnect with
	// backoff on failure, until ctx is canceled. Runs in the background so the
	// signal handler below stays responsive.
	log.Info("Starting agent connection supervisor", "agent_id", agentID)
	go func() {
		if err := agent.Run(ctx); errors.Is(err, grpcagent.ErrRevoked) {
			log.Error("The hub no longer accepts this server; stopping for good. Re-enroll it with a new code.", "error", err)
			os.Exit(grpcagent.ExitRevoked)
		}
	}()

	// Periodic apps-status push: feeds the API's status cache and the SSE
	// live-status pipeline. Ticks while the stream is down are skipped.
	go appagent.RunStatusReporter(ctx, statusSnapshot, sink, 30*time.Second, backups.Changed(), log)

	// Resource metrics: a server.metrics event every 60 s (procfs/cgroupfs
	// reads, no forks) plus the hourly disk scan; off with
	// METRICS_ENABLED=false.
	appagent.StartMetrics(ctx, cfg, statusSnapshot, orchestrator, sink, log)

	// Auto-update for git-sourced apps: polls upstreams on each app's own
	// interval and redeploys on new commits.
	go appagent.RunSourcePoller(ctx, orchestrator, appagent.AfterSourceUpdate(ctx, backups.Reload, ingressManager, log), log)

	// Status monitoring routine
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				log.Info("Agent status",
					"agent_id", agentID,
					"connected", agent.IsConnected(),
					"registered", agent.IsRegistered(),
					"stream_active", agent.IsStreamActive())
			}
		}
	}()

	log.Info("Agent is running", "agent_id", agentID)

	<-ctx.Done()
	log.Info("Shutdown signal received, starting graceful shutdown")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := agent.Stop(shutdownCtx); err != nil {
		log.Error("Error during shutdown", "error", err)
		os.Exit(1)
	}
	log.Info("Agent gracefully stopped")
}
