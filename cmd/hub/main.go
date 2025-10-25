package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"
	grpchub "winterflow/internal/infra/transport/grpc/hub"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	err := godotenv.Load()
	cfg := config.NewConfig()
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

	//container := bootstrap.BootstrapHUB(ctx, log, cfg)
	hub := grpchub.NewHub(log, cfg)

	go func() {
		if err := hub.ListenAndServe(ctx); err != nil {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	log.Info("Shutdown signal received, starting graceful shutdown")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := hub.Shutdown(shutdownCtx); err != nil {
		log.Fatal("Error during shutdown", "error", err)
	}
	log.Info("Server gracefully stopped")
}
