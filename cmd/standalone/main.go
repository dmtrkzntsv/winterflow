package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"winterflow/internal/app/web"
	"winterflow/internal/infra/bootstrap"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"github.com/joho/godotenv"
)

// systemEnvFile is the config written by scripts/install.sh. Loading it as a
// fallback lets the installed `winterflow` CLI run from any directory, not
// just one containing a .env.
const systemEnvFile = "/etc/winterflow/winterflow.env"

func main() {
	envFile := flag.String("env-file", "", "env file to load (default: ./.env, then "+systemEnvFile+")")
	flag.Parse()

	var err error
	if *envFile != "" {
		err = godotenv.Load(*envFile)
	} else if err = godotenv.Load(); err != nil {
		err = godotenv.Load(systemEnvFile)
	}
	cfg := config.NewServerConfig("standalone")
	log := logger.NewLogger(logger.LoggerConfiguration{
		LogLevel: os.Getenv("LOG_LEVEL"),
		Service:  "winterflow",
	})
	if err != nil {
		if *envFile != "" {
			log.Fatalf("failed to load env file %s: %v", *envFile, err)
		}
		log.Info("no .env or " + systemEnvFile + " found, using system environment variables")
	}
	log.Info("Service starting", "pid", os.Getpid())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	deps := bootstrap.BootstrapStandalone(ctx, log, cfg)
	srv := web.NewServer(ctx, deps)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatal(err)
	}
	log.Info("server gracefully stopped")
}
