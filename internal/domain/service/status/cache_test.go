package status

import (
	"testing"
	"time"

	"winterflow/internal/domain/command"
)

func TestServerLivenessFreshIsOnline(t *testing.T) {
	c := NewCache(60 * time.Second)
	now := time.Unix(1000, 0)
	c.MarkOnline("srv-1", now)

	if got := c.ServerLiveness("srv-1", now.Add(30*time.Second)); got != LivenessOnline {
		t.Errorf("within TTL: got %q, want online", got)
	}
}

func TestServerLivenessExpiredIsUnknown(t *testing.T) {
	c := NewCache(60 * time.Second)
	now := time.Unix(1000, 0)
	c.MarkOnline("srv-1", now)

	// Past the TTL — must be unknown, never "offline".
	if got := c.ServerLiveness("srv-1", now.Add(61*time.Second)); got != LivenessUnknown {
		t.Errorf("past TTL: got %q, want unknown", got)
	}
}

func TestServerLivenessAbsentIsUnknown(t *testing.T) {
	c := NewCache(60 * time.Second)
	if got := c.ServerLiveness("never-seen", time.Unix(1000, 0)); got != LivenessUnknown {
		t.Errorf("absent: got %q, want unknown", got)
	}
}

func TestAppStatusesFreshThenExpire(t *testing.T) {
	c := NewCache(60 * time.Second)
	now := time.Unix(1000, 0)
	c.SetAppStatus("srv-1", []command.AppStatus{{AppID: "a1", StatusCode: command.ContainerStatusActive}}, now)

	got := c.AppStatuses("srv-1", now.Add(30*time.Second))
	if len(got) != 1 || got[0].AppID != "a1" {
		t.Fatalf("within TTL: got %+v", got)
	}

	if got := c.AppStatuses("srv-1", now.Add(61*time.Second)); got != nil {
		t.Errorf("past TTL: got %+v, want nil", got)
	}
}

func TestSetAppStatusAlsoMarksOnline(t *testing.T) {
	c := NewCache(60 * time.Second)
	now := time.Unix(1000, 0)
	c.SetAppStatus("srv-1", nil, now)
	if got := c.ServerLiveness("srv-1", now); got != LivenessOnline {
		t.Errorf("a status report should also be a liveness signal, got %q", got)
	}
}
