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
		runSourcePoller(ctx, f, nil, 10*time.Millisecond, log)
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

// An auto-update can change an app's backup recipe: the scheduler's plan is
// reloaded after a tick that updated apps, and not after one that didn't.
func TestSourcePollerReloadsBackupsAfterAnUpdate(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	run := func(updated []string) int32 {
		ctx, cancel := context.WithCancel(context.Background())
		f := &countingRefresher{updated: updated}
		var reloads atomic.Int32
		done := make(chan struct{})
		go func() {
			runSourcePoller(ctx, f, func() { reloads.Add(1) }, 5*time.Millisecond, log)
			close(done)
		}()
		deadline := time.After(2 * time.Second)
		for f.calls.Load() < 3 {
			select {
			case <-deadline:
				t.Fatal("poller did not tick")
			case <-time.After(2 * time.Millisecond):
			}
		}
		cancel()
		<-done
		return reloads.Load()
	}
	if got := run([]string{"app-1"}); got < 1 {
		t.Errorf("updated: %d reloads, want >= 1", got)
	}
	if got := run(nil); got != 0 {
		t.Errorf("nothing updated: %d reloads, want 0", got)
	}
}

type countingRefresher struct {
	calls   atomic.Int32
	updated []string
}

func (f *countingRefresher) RefreshDueSources(context.Context) []string {
	f.calls.Add(1)
	return f.updated
}
