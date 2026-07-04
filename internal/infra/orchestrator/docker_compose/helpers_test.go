package dockercompose

import (
	"testing"

	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// newTestRepo builds a Repository rooted at a throwaway data dir.
func newTestRepo(t *testing.T) *Repository {
	t.Helper()
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	return NewRepository(
		config.NewServerConfig("standalone"),
		logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
	)
}
