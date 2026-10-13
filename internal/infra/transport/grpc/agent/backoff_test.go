package agent

import (
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
