package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"winterflow/pkg/logger"
)

// counter is an hourly job that reports each run.
func counter(name string, runs chan string, err error) hourlyJob {
	return hourlyJob{name: name, run: func(context.Context) error {
		select {
		case runs <- name:
		default:
		}
		return err
	}}
}

// The first run comes shortly after start, not a full interval later: a
// process restarted more often than hourly must still prune; every job
// runs even when an earlier one fails.
func TestHourlyMaintenanceRunsSoonAfterStart(t *testing.T) {
	if hourlyFirst > 5*time.Minute || hourlyFirst >= hourlyInterval {
		t.Fatalf("first run after %v (interval %v)", hourlyFirst, hourlyInterval)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runs := make(chan string, 8)
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"})
	jobs := []hourlyJob{counter("tokens", runs, errors.New("db down")), counter("results", runs, nil)}
	go runHourlyMaintenance(ctx, jobs, log, 10*time.Millisecond, 20*time.Millisecond)
	for i, want := range []string{"tokens", "results", "tokens", "results"} {
		select {
		case got := <-runs:
			if got != want {
				t.Fatalf("run %d = %s, want %s", i+1, got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("run %d (%s) did not happen", i+1, want)
		}
	}
}
