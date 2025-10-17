package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"winterflow/internal/app"
	"winterflow/internal/app/web"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	err := godotenv.Load()
	log := logger.NewLogger(logger.LoggerConfiguration{
		LogLevel: os.Getenv("LOG_LEVEL"),
		Service:  "standalone",
	})
	if err != nil {
		log.Info(".env not found, using system environment variables")
	}
	log.Info("Standalone service starting", "pid", os.Getpid())
	cfg := config.NewConfig()

	srv := web.NewServer(app.AppModeStandalone, log, cfg)

	// Create context that listens for the interrupt signal
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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
