package bootstrap

import (
	"context"
	"testing"
	"time"

	"winterflow/pkg/logger"
)

type countingPruner chan struct{}

func (c countingPruner) Prune(context.Context) error {
	select {
	case c <- struct{}{}:
	default:
	}
	return nil
}

// The first prune runs shortly after start, not a full interval later: a
// process restarted more often than hourly must still prune.
func TestTokenMaintenancePrunesSoonAfterStart(t *testing.T) {
	if tokenPruneFirst > 5*time.Minute || tokenPruneFirst >= tokenPruneInterval {
		t.Fatalf("first prune after %v (interval %v)", tokenPruneFirst, tokenPruneInterval)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(countingPruner, 4)
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"})
	go runTokenMaintenance(ctx, calls, log, 10*time.Millisecond, 20*time.Millisecond)
	for i := range 2 {
		select {
		case <-calls:
		case <-time.After(5 * time.Second):
			t.Fatalf("prune %d did not run", i+1)
		}
	}
}
