package main

import (
	"context"
	"errors"
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

func main() {
	err := godotenv.Load()
	cfg := config.NewServerConfig("distributed")
	log := logger.NewLogger(logger.LoggerConfiguration{
		LogLevel: os.Getenv("LOG_LEVEL"),
		Service:  "winterflow-api",
	})
	if err != nil {
		log.Info(".env not found, using system environment variables")
	}

	// Hidden maintenance subcommand: print a one-time hub join token.
	if len(os.Args) > 1 && os.Args[1] == "hub-token" {
		if err := cfg.ValidateDatabaseURL(); err != nil {
			log.Fatalf("invalid configuration: %v", err)
		}
		if err := runHubToken(os.Args[2:], cfg.GetDbURL(), log, os.Stdout); err != nil {
			log.Fatalf("hub-token: %v", err)
		}
		return
	}

	log.Info("Service starting", "pid", os.Getpid())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	deps := bootstrap.BootstrapAPI(ctx, log, cfg)
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
