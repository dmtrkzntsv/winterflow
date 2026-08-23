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

func TestMarkOnlineReportsTransition(t *testing.T) {
	c := NewCache(60 * time.Second)
	now := time.Unix(1000, 0)

	if !c.MarkOnline("srv-1", now) {
		t.Error("first pulse: want transition (unknown -> online)")
	}
	if c.MarkOnline("srv-1", now.Add(30*time.Second)) {
		t.Error("refresh within TTL: want no transition")
	}
	// Entry expired without a sweep: the next pulse is a fresh transition.
	if !c.MarkOnline("srv-1", now.Add(200*time.Second)) {
		t.Error("pulse after expiry: want transition")
	}
}

func TestSetAppStatusReportsLivenessTransition(t *testing.T) {
	c := NewCache(60 * time.Second)
	now := time.Unix(1000, 0)

	if _, transition := c.SetAppStatus("srv-1", nil, now); !transition {
		t.Error("first status report: want transition")
	}
	if _, transition := c.SetAppStatus("srv-1", nil, now.Add(10*time.Second)); transition {
		t.Error("second report within TTL: want no transition")
	}
}

func TestSetAppStatusReportsContentChange(t *testing.T) {
	c := NewCache(60 * time.Second)
	now := time.Unix(1000, 0)
	active := []command.AppStatus{{AppID: "a1", StatusCode: command.ContainerStatusActive}}
	stopped := []command.AppStatus{{AppID: "a1", StatusCode: command.ContainerStatusStopped}}

	if changed, _ := c.SetAppStatus("srv-1", active, now); !changed {
		t.Error("first report: want changed")
	}
	// Identical snapshot on the next tick: no change, no fan-out needed.
	if changed, _ := c.SetAppStatus("srv-1", active, now.Add(30*time.Second)); changed {
		t.Error("identical report: want unchanged")
	}
	if changed, _ := c.SetAppStatus("srv-1", stopped, now.Add(60*time.Second)); !changed {
		t.Error("different report: want changed")
	}
}

func TestExpireStaleReturnsEachFlipOnce(t *testing.T) {
	c := NewCache(60 * time.Second)
	now := time.Unix(1000, 0)
	c.MarkOnline("srv-1", now)
	c.MarkOnline("srv-2", now.Add(30*time.Second))

	if got := c.ExpireStale(now.Add(45 * time.Second)); len(got) != 0 {
		t.Fatalf("nothing expired yet, got %v", got)
	}

	got := c.ExpireStale(now.Add(70 * time.Second))
	if len(got) != 1 || got[0] != "srv-1" {
		t.Fatalf("want [srv-1], got %v", got)
	}
	// A second sweep must not report srv-1 again.
	if got := c.ExpireStale(now.Add(75 * time.Second)); len(got) != 0 {
		t.Fatalf("srv-1 already reported, got %v", got)
	}
	// srv-2 expires later.
	got = c.ExpireStale(now.Add(120 * time.Second))
	if len(got) != 1 || got[0] != "srv-2" {
		t.Fatalf("want [srv-2], got %v", got)
	}
}

func TestExpireStaleThenReonlineTransitionsAgain(t *testing.T) {
	c := NewCache(60 * time.Second)
	now := time.Unix(1000, 0)
	c.MarkOnline("srv-1", now)
	c.ExpireStale(now.Add(90 * time.Second))

	if !c.MarkOnline("srv-1", now.Add(100*time.Second)) {
		t.Error("re-online after sweep: want transition")
	}
	if got := c.ExpireStale(now.Add(200 * time.Second)); len(got) != 1 || got[0] != "srv-1" {
		t.Fatalf("second expiry cycle: want [srv-1], got %v", got)
	}
}
