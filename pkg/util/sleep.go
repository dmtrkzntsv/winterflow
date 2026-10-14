package util

import (
	"context"
	"time"
)

// Sleep waits d unless ctx ends first; it reports whether ctx is still
// live (false: cancelled before d elapsed).
func Sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
