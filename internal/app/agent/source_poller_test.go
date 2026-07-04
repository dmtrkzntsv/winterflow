package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"winterflow/pkg/logger"
)

type fakeRefresher struct {
	calls atomic.Int32
}

func (f *fakeRefresher) RefreshDueSources(context.Context) []string {
	f.calls.Add(1)
	return []string{"app-1"}
}

func TestSourcePollerTicksAndStops(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	ctx, cancel := context.WithCancel(context.Background())
	f := &fakeRefresher{}

	done := make(chan struct{})
	go func() {
		runSourcePoller(ctx, f, 10*time.Millisecond, log)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for f.calls.Load() < 2 {
		select {
		case <-deadline:
			t.Fatalf("poller ticked %d times, want >= 2", f.calls.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("poller did not stop on cancel")
	}
}
