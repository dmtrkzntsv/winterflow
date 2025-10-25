package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"winterflow/internal/infra/transport/grpc/hub"
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
	hub := hub.NewHub(log, cfg)

	go func() {
		if err := hub.ListenAndServe(ctx); err != nil {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()

	//shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	//defer cancel()

	if err := hub.Shutdown(); err != nil {
		log.Fatal(err)
	}
	log.Info("server gracefully stopped")
}
