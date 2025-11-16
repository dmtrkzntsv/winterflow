package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
	grpcagent "winterflow/internal/infra/transport/grpc/agent"
	"winterflow/internal/infra/transport/grpc/proto"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
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

	// Generate agent ID (in production, this might come from config or be persistent)
	agentID := fmt.Sprintf("agent-%d", time.Now().Unix())

	agent := grpcagent.NewAgent(log, cfg, agentID)

	// Set agent capabilities and features
	capabilities := map[string]string{
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
		"go_version": runtime.Version(),
		"hostname":   getHostname(),
	}

	features := map[string]bool{
		"can_install":    true,
		"can_execute":    true,
		"can_fetch_logs": true,
		"can_monitor":    true,
	}

	apps := []*proto.App{
		{
			AppId:           "winterflow-agent",
			Name:            "Winterflow Agent",
			Version:         "1.0.0",
			ProtocolVersion: "1.0.0",
		},
	}

	agent.SetCapabilities(capabilities)
	agent.SetFeatures(features)
	agent.SetApps(apps)

	// Connect to hub
	log.Info("Connecting to hub", "agent_id", agentID)
	if err := agent.Connect(ctx); err != nil {
		log.Fatal("Failed to connect to hub", "error", err)
	}

	// Register with hub
	log.Info("Registering with hub", "agent_id", agentID)
	if err := agent.Register(ctx); err != nil {
		log.Fatal("Failed to register with hub", "error", err)
	}

	// Start streaming
	log.Info("Starting agent stream", "agent_id", agentID)
	if err := agent.StartStream(ctx); err != nil {
		log.Fatal("Failed to start stream", "error", err)
	}

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

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}
