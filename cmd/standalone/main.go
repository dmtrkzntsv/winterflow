package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"winterflow/internal/app/web"
	"winterflow/internal/infra/bootstrap"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
	"winterflow/pkg/version"

	"github.com/joho/godotenv"
)

// systemEnvFile is the config written by scripts/install.sh. Loading it as a
// fallback lets the installed `winterflow` CLI run from any directory, not
// just one containing a .env.
const systemEnvFile = "/etc/winterflow/winterflow.env"

func usage() {
	fmt.Fprintf(os.Stderr, `WinterFlow standalone — API, web UI, agent and orchestrator in one process.

Usage:
  winterflow <command> [flags]

Commands:
  serve     run the server (flags: -env-file PATH)
  version   print the build version
  help      show this help
`)
}

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
	case "serve":
		serve(args)
	case "version", "-v", "--version":
		fmt.Println(version.GetVersion())
	case "help", "-h", "--help":
		usage()
	case "":
		usage()
		os.Exit(2)
	default:
		fmt.Fprintf(os.Stderr, "winterflow: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func serve(args []string) {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	envFile := flags.String("env-file", "", "env file to load (default: ./.env, then "+systemEnvFile+")")
	_ = flags.Parse(args)

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
	log.Info("Service starting", "pid", os.Getpid(), "version", version.GetVersion())

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
