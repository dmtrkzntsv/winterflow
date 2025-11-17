package port

import (
	"context"
)

type AgentService interface {
	HasConfig(ctx context.Context) bool
	GenerateConfig(ctx context.Context) error
	// HasKeys(ctx context.Context) bool
	// GenerateKeys(ctx context.Context) error

	// IsRegistered(ctx context.Context) bool
	// Register(ctx context.Context) (string, error)
	// GetCapabilities(ctx context.Context) error
	// GetMetrics(ctx context.Context) error
}
