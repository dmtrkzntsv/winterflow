package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"winterflow/pkg/logger"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errDenied = fmt.Errorf("register: %w", status.Error(codes.PermissionDenied, "server revoked"))

type step struct {
	err    error // bring-up error
	endErr error // how the stream ended when err is nil
}

// scripted agent: each bring-up pops a step; after the script runs out it
// cancels ctx. Recorded waits come from deniedBackoff entries (tiny, distinct).
func scripted(t *testing.T, steps []step) (*Agent, context.Context, *int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a := NewAgent(logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "t"}), nil, "a")
	a.deniedBackoff = []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, 4 * time.Millisecond}
	a.retryBackoff = &[2]time.Duration{time.Millisecond, 2 * time.Millisecond}
	var mu sync.Mutex
	n := 0
	a.bringUp = func(ctx context.Context) (<-chan struct{}, func() error, error) {
		mu.Lock()
		defer mu.Unlock()
		if n >= len(steps) {
			cancel()
			return nil, nil, errors.New("script over")
		}
		s := steps[n]
		n++
		if s.err != nil {
			return nil, nil, s.err
		}
		done := make(chan struct{})
		close(done)
		return done, func() error { return s.endErr }, nil
	}
	return a, ctx, &n
}

func TestRunStopsAfterFiveDenials(t *testing.T) {
	a, ctx, n := scripted(t, []step{{err: errDenied}, {err: errDenied}, {err: errDenied}, {err: errDenied}, {err: errDenied}, {err: errDenied}})
	if err := a.Run(ctx); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Run = %v, want ErrRevoked", err)
	}
	if *n != 5 {
		t.Fatalf("attempts = %d, want 5", *n)
	}
}

func TestRunCountsRevokedLiveStream(t *testing.T) {
	denied := status.Error(codes.PermissionDenied, "server revoked")
	steps := make([]step, 5)
	for i := range steps {
		steps[i] = step{endErr: denied}
	}
	a, ctx, n := scripted(t, steps)
	if err := a.Run(ctx); !errors.Is(err, ErrRevoked) || *n != 5 {
		t.Fatalf("Run = %v after %d attempts, want ErrRevoked after 5", err, *n)
	}
}

func TestRunUnavailableNeverStops(t *testing.T) {
	unavailable := fmt.Errorf("connect: %w", status.Error(codes.Unavailable, "x"))
	steps := make([]step, 8)
	for i := range steps {
		steps[i] = step{err: unavailable}
	}
	a, ctx, _ := scripted(t, steps)
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
}

func TestRunMixedDeniedAndUnavailableKeepsCount(t *testing.T) {
	un := status.Error(codes.Unavailable, "x")
	a, ctx, _ := scripted(t, []step{{err: errDenied}, {err: un}, {err: errDenied}, {err: un}, {err: errDenied}, {err: errDenied}, {err: errDenied}})
	if err := a.Run(ctx); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Run = %v, want ErrRevoked", err)
	}
}

func TestRunSuccessResetsDenials(t *testing.T) {
	steps := []step{{err: errDenied}, {err: errDenied}, {err: errDenied}, {}, // stream up, ends without a denial
		{err: errDenied}, {err: errDenied}, {err: errDenied}, {err: errDenied}}
	a, ctx, n := scripted(t, steps)
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil (still running)", err)
	}
	if *n != len(steps) {
		t.Fatalf("attempts = %d, want the whole script consumed", *n)
	}
}
