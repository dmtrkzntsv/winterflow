package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"winterflow/pkg/logger"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errDenied = fmt.Errorf("register: %w", status.Error(codes.PermissionDenied, "server revoked"))

type step struct {
	err    error         // bring-up error
	endErr error         // how the stream ended when err is nil
	lived  time.Duration // how long the stream stayed up (fake clock)
}

type harness struct {
	n     int
	waits []time.Duration // every wait Run asked for (denied or ordinary)
}

var testDenied = []time.Duration{time.Minute, 2 * time.Minute, 3 * time.Minute, 4 * time.Minute}

// scripted builds an agent whose bring-ups follow steps; once the script is
// over the next bring-up cancels ctx. Sleeps are recorded, not taken, and the
// clock is fake.
func scripted(t *testing.T, steps []step) (*Agent, context.Context, *harness) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a := NewAgent(logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "t"}), nil, "a")
	a.deniedBackoff = testDenied
	a.retryBackoff = &[2]time.Duration{time.Millisecond, 2 * time.Millisecond}
	a.settle = 2 * time.Minute
	h := &harness{}
	clock := time.Unix(0, 0)
	a.now = func() time.Time { return clock }
	a.sleep = func(_ context.Context, d time.Duration) bool {
		h.waits = append(h.waits, d)
		return ctx.Err() == nil
	}
	a.bringUp = func(ctx context.Context) (<-chan struct{}, func() error, error) {
		if h.n >= len(steps) {
			cancel()
			return nil, nil, errors.New("script over")
		}
		s := steps[h.n]
		h.n++
		if s.err != nil {
			return nil, nil, s.err
		}
		done := make(chan struct{})
		close(done)
		// The stream "lives" until Run asks how it ended.
		return done, func() error { clock = clock.Add(s.lived); return s.endErr }, nil
	}
	return a, ctx, h
}

func TestRunStopsAfterFiveDenials(t *testing.T) {
	a, ctx, h := scripted(t, []step{{err: errDenied}, {err: errDenied}, {err: errDenied}, {err: errDenied}, {err: errDenied}, {err: errDenied}})
	if err := a.Run(ctx); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Run = %v, want ErrRevoked", err)
	}
	if h.n != 5 {
		t.Fatalf("attempts = %d, want 5", h.n)
	}
	if fmt.Sprint(h.waits) != fmt.Sprint(testDenied) {
		t.Fatalf("waits = %v, want %v", h.waits, testDenied)
	}
}

func TestRunCountsRevokedLiveStream(t *testing.T) {
	denied := status.Error(codes.PermissionDenied, "server revoked")
	steps := make([]step, 5)
	for i := range steps {
		steps[i] = step{endErr: denied}
	}
	a, ctx, h := scripted(t, steps)
	if err := a.Run(ctx); !errors.Is(err, ErrRevoked) || h.n != 5 {
		t.Fatalf("Run = %v after %d attempts, want ErrRevoked after 5", err, h.n)
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
	a, ctx, h := scripted(t, steps)
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil (still running)", err)
	}
	if h.n != len(steps) {
		t.Fatalf("attempts = %d, want the whole script consumed", h.n)
	}
}

func TestRunLongStreamResetsDenialsEvenWhenItEndsDenied(t *testing.T) {
	revoke := status.Error(codes.PermissionDenied, "server revoked")
	a, ctx, h := scripted(t, []step{
		{err: errDenied}, {err: errDenied}, {err: errDenied}, {err: errDenied},
		{endErr: revoke, lived: 24 * time.Hour},
	})
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil (still running)", err)
	}
	// The revoke is denial number 1 again: it waits the first denied backoff.
	if got := h.waits[4]; got != testDenied[0] {
		t.Fatalf("wait after the revoke = %v, want %v (waits %v)", got, testDenied[0], h.waits)
	}
}

func TestRunQuickDeniedStreamsAccumulate(t *testing.T) {
	revoke := status.Error(codes.PermissionDenied, "server revoked")
	steps := make([]step, 5)
	for i := range steps {
		steps[i] = step{endErr: revoke, lived: time.Second}
	}
	a, ctx, h := scripted(t, steps)
	if err := a.Run(ctx); !errors.Is(err, ErrRevoked) || h.n != 5 {
		t.Fatalf("Run = %v after %d attempts, want ErrRevoked after 5", err, h.n)
	}
}
