package main

import (
	"context"
	"fmt"
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
	go func() {
		log.Info(fmt.Sprintf("Starting server on port %s", cfg.GetServerPort()))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Server failed", "error", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Info("server gracefully stopped")
}
