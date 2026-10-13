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

func TestVarsRevision(t *testing.T) {
	c := NewCache(time.Minute)
	if _, ok := c.VarsRevision("s1"); ok {
		t.Fatal("unknown server has a revision")
	}
	c.SetVarsRevision("s1", 4)
	if rev, ok := c.VarsRevision("s1"); !ok || rev != 4 {
		t.Fatalf("revision = %d %v", rev, ok)
	}
	// Liveness expiry does not forget the revision (it is not liveness).
	c.ExpireStale(time.Now().Add(time.Hour))
	if _, ok := c.VarsRevision("s1"); !ok {
		t.Fatal("revision expired with liveness")
	}
}

func TestLatestReportKeepsTimeAndRevision(t *testing.T) {
	c := NewCache(60 * time.Second)
	now := time.Unix(1000, 0)
	if _, ok := c.LatestReport("srv-1", now); ok {
		t.Fatal("a report before any arrived")
	}
	rev := int64(7)
	apps := []command.AppStatus{{AppID: "a1", StatusCode: command.ContainerStatusActive, StaleVars: []string{"SMTP_PASS"}}}
	c.SetAppReport("srv-1", apps, &rev, now)
	got, ok := c.LatestReport("srv-1", now.Add(10*time.Second))
	if !ok || !got.Received.Equal(now) || !got.HasRevision || got.VarsRevision != 7 || len(got.Apps) != 1 || got.Apps[0].StaleVars[0] != "SMTP_PASS" {
		t.Fatalf("report = %+v, %v", got, ok)
	}
	// An agent predating shared variables reports no revision.
	c.SetAppStatus("srv-1", apps, now.Add(20*time.Second))
	got, _ = c.LatestReport("srv-1", now.Add(20*time.Second))
	if got.HasRevision || !got.Received.Equal(now.Add(20*time.Second)) {
		t.Fatalf("report without a revision = %+v", got)
	}
	if _, ok := c.LatestReport("srv-1", now.Add(81*time.Second)); ok {
		t.Fatal("an expired report is still served")
	}
}
