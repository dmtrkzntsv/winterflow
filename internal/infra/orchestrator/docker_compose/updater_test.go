package dockercompose

import (
	"context"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func newUpdaterTestRepo(t *testing.T) *Repository {
	t.Helper()
	return NewRepository(
		config.NewServerConfig("standalone"),
		logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
	)
}

func TestUpdateAgentNoopWhenNotNewer(t *testing.T) {
	r := newUpdaterTestRepo(t)
	// Default build version is "0.0.0"; an equal/older target is a no-op and must
	// not download or exit.
	res, err := r.UpdateAgent(context.Background(), command.UpdateAgentRequest{Version: "0.0.0"})
	if err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	if res.Scheduled {
		t.Error("update should not be scheduled when target is not newer")
	}
}

func TestUpdateAgentRequiresVersion(t *testing.T) {
	r := newUpdaterTestRepo(t)
	if _, err := r.UpdateAgent(context.Background(), command.UpdateAgentRequest{}); err == nil {
		t.Error("expected error for empty target version")
	}
}
