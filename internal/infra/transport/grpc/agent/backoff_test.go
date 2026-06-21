package agent

import (
	"context"
	"testing"
	"time"
)

func TestNextBackoffDoublesAndCaps(t *testing.T) {
	max := 30 * time.Second
	cases := []struct {
		in, want time.Duration
	}{
		{1 * time.Second, 2 * time.Second},
		{2 * time.Second, 4 * time.Second},
		{16 * time.Second, 30 * time.Second}, // 32 capped to 30
		{30 * time.Second, 30 * time.Second}, // stays at cap
	}
	for _, c := range cases {
		if got := nextBackoff(c.in, max); got != c.want {
			t.Errorf("nextBackoff(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSleepCtxReturnsFalseOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepCtx(ctx, time.Hour) {
		t.Error("sleepCtx should return false when ctx is already canceled")
	}
}

func TestSleepCtxReturnsTrueOnElapse(t *testing.T) {
	if !sleepCtx(context.Background(), time.Millisecond) {
		t.Error("sleepCtx should return true after the duration elapses")
	}
}
