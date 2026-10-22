package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"winterflow/internal/app/web"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/bootstrap"
	redisbus "winterflow/internal/infra/transport/redis/bus"
	"winterflow/internal/infra/transport/redis/hubusers"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"github.com/joho/godotenv"
)

// subcommands are the hidden maintenance commands, by name.
func subcommands(cfg *config.ServerConfig) map[string]func(args []string, dbURL string, log *logger.Logger, out io.Writer) error {
	return map[string]func(args []string, dbURL string, log *logger.Logger, out io.Writer) error{
		"hub-token": runHubToken,
		"hub-trust": func(args []string, dbURL string, log *logger.Logger, out io.Writer) error {
			return runHubTrust(args, dbURL, cfg.GetHubCACertPath(), openHubUsers(cfg), log, out)
		},
		"hub-retire": func(args []string, dbURL string, log *logger.Logger, out io.Writer) error {
			return runHubRetire(args, dbURL, openHubUsers(cfg), log, out)
		},
	}
}

// openHubUsers connects to Redis with the API's settings (the admin user
// that manages the hubs' users).
func openHubUsers(cfg *config.ServerConfig) openUsersFunc {
	return func(ctx context.Context) (port.HubRedisUsers, func(), error) {
		rc, err := redisbus.NewRedisClient(cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("redis configuration: %w", err)
		}
		if err := redisbus.Ping(ctx, rc); err != nil {
			_ = rc.Close()
			addr, _, _ := cfg.GetRedisCredentials()
			return nil, nil, fmt.Errorf("connect to redis at %s: %w", addr, err)
		}
		return hubusers.New(rc), func() { _ = rc.Close() }, nil
	}
}

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

	// Hidden maintenance subcommands: print a one-time hub join token;
	// trust or retire hub certificates.
	if len(os.Args) > 1 {
		if run, ok := subcommands(cfg)[os.Args[1]]; ok {
			if err := cfg.ValidateDatabaseURL(); err != nil {
				log.Fatalf("invalid configuration: %v", err)
			}
			if err := run(os.Args[2:], cfg.GetDbURL(), log, os.Stdout); err != nil {
				log.Fatalf("%s: %v", os.Args[1], err)
			}
			return
		}
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
