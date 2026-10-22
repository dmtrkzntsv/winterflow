package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
	"winterflow/internal/infra/bootstrap"
	grpchub "winterflow/internal/infra/transport/grpc/hub"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	// Hidden subcommand for the installers: issue the hub/Redis PKI and exit.
	if len(os.Args) > 1 && os.Args[1] == "pki" {
		if err := runPKI(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// Hidden subcommand for local development (make generate-agent-certs): a
	// CA-signed agent certificate for a given server id.
	if len(os.Args) > 1 && os.Args[1] == "dev-agent-cert" {
		if err := runDevAgentCert(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// Hidden subcommand for the hub installer: redeem a join token for the
	// hub's certificate (exit 2: the control plane refused the token).
	if len(os.Args) > 1 && os.Args[1] == "join" {
		if err := runJoin(os.Args[2:], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			var je *joinError
			if errors.As(err, &je) {
				os.Exit(je.code)
			}
			os.Exit(1)
		}
		return
	}
	// Hidden subcommands: drain (move this hub's agents to other hubs and
	// refuse new ones) and undrain, through the flag in Redis.
	if len(os.Args) > 1 && (os.Args[1] == "drain" || os.Args[1] == "undrain") {
		_ = godotenv.Load()
		if err := runDrain(os.Args[1] == "drain", os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	err := godotenv.Load()
	cfg := config.NewServerConfig("distributed")
	log := logger.NewLogger(logger.LoggerConfiguration{
		LogLevel: os.Getenv("LOG_LEVEL"),
		Service:  "winterflow-hub",
	})
	if err != nil {
		log.Info(".env not found, using system environment variables")
	}
	log.Info("Service starting", "pid", os.Getpid())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// runCtx outlives the signal: while the hub drains on stop it keeps
	// forwarding commands to the agents still on it, answering auth checks
	// and announcing itself (as draining). It ends after Shutdown.
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	hubDeps := bootstrap.BootstrapHUB(runCtx, log, cfg)
	hub := grpchub.NewHub(log, cfg, hubDeps.Bus, hubDeps.Auth)

	if err := hub.StartBusBridge(runCtx); err != nil {
		log.Fatal("Failed to start bus bridge", "error", err)
	}

	// Lists the hub in Redis (hub:status:<hub_id>) so the API can assign
	// servers to it; removed again when runCtx ends.
	announced := hub.StartAnnouncer(runCtx, hubDeps.Registry)
	// Follows hub:draining:<hub_id> (winterflow-hub drain / undrain).
	watched := hub.StartDrainWatch(runCtx, hubDeps.Registry)

	go func() {
		if err := hub.ListenAndServe(runCtx); err != nil {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	log.Info("Shutdown signal received, draining then stopping")

	// Draining takes at most grpchub.DrainTimeout; the rest is for the
	// graceful stop. The unit's TimeoutStopSec=150 covers both.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), grpchub.DrainTimeout+15*time.Second)
	defer cancel()

	shutdownErr := hub.Shutdown(shutdownCtx)
	cancelRun()
	<-announced // the entry's removal is bounded by 3 s
	<-watched
	if shutdownErr != nil {
		log.Fatal("Error during shutdown", "error", shutdownErr)
	}
	log.Info("Server gracefully stopped")
}
