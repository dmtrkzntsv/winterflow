package alerting

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/alert"
	"winterflow/internal/domain/metrics"
)

func cpuRule(s *fakeStore, threshold float64, minutes int) alert.Rule {
	return s.addRule(alert.Rule{TargetKind: alert.TargetServer, Metric: alert.MetricCPU, Threshold: threshold, DurationMinutes: minutes})
}

func TestThresholdFiresAndResolves(t *testing.T) {
	store := newStore()
	rule := cpuRule(store, 90, 10)
	h := newHarness(store)
	for i := 0; i < 7; i++ {
		h.sample(95, nil)
	}
	if len(store.events) != 0 {
		t.Fatalf("fired on 7 of 10 samples: %v", store.eventKinds())
	}
	h.sample(95, nil) // 8 of 10: enough coverage
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"fired:cpu:"}) {
		t.Fatalf("events = %v", got)
	}
	ev := store.events[0]
	if ev.Title != "🔥 prod-1 · CPU 95% > 90% for 10m" || ev.ServerName != "prod-1" || ev.Threshold != 90 ||
		!strings.Contains(ev.Message, "https://wf.example/server/srv-1/monitoring") {
		t.Fatalf("event = %+v", ev)
	}
	for i := 0; i < 3; i++ { // keeps firing, no repeats
		h.sample(95, nil)
	}
	h.sample(50, nil) // last 10: 9×95 + 50 → 90.5, still above
	if len(store.events) != 1 {
		t.Fatalf("events = %v", store.eventKinds())
	}
	h.sample(50, nil) // 8×95 + 2×50 → 86
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"fired:cpu:", "resolved:cpu:"}) {
		t.Fatalf("events = %v", got)
	}
	if h.queue.len() != 2 || len(h.notices) != 2 || h.notices[1].Kind != alert.EventResolved || h.notices[1].RuleID != rule.ID {
		t.Fatalf("jobs=%d notices=%+v", h.queue.len(), h.notices)
	}
	if h.queue.jobs[0].ChannelIDs != nil || h.queue.jobs[0].OrgID != org {
		t.Fatalf("job = %+v", h.queue.jobs[0])
	}
}

func TestThresholdKeepsStateWithoutCoverage(t *testing.T) {
	store := newStore()
	cpuRule(store, 90, 5)
	h := newHarness(store)
	for i := 0; i < 5; i++ {
		h.sample(99, nil)
	}
	if len(store.events) != 1 {
		t.Fatalf("events = %v", store.eventKinds())
	}
	// The server goes quiet for 20 minutes, then one low sample: one point
	// in a 5-minute window is not a decision either way.
	h.clock.t = h.clock.t.Add(20 * time.Minute)
	h.sample(10, nil)
	if len(store.events) != 1 {
		t.Fatalf("decided on a sparse window: %v", store.eventKinds())
	}
	// A sample without the metric (first one after a restart has no CPU)
	// does not count either.
	h.ev.ObserveMetrics(context.Background(), srv, h.clock.t, metrics.Values{"mem_used": 1}, nil)
	if len(store.events) != 1 {
		t.Fatalf("events = %v", store.eventKinds())
	}
}

func TestOverrideChangesThresholdAndDisables(t *testing.T) {
	store := newStore()
	rule := cpuRule(store, 90, 2)
	high := 99.0
	store.overrides = []alert.Override{{RuleID: rule.ID, ServerID: srv, Threshold: &high, ChannelIDs: []string{"ch-9"}}}
	h := newHarness(store)
	h.sample(95, nil)
	h.sample(95, nil)
	if len(store.events) != 0 {
		t.Fatalf("override threshold ignored: %v", store.eventKinds())
	}
	h.sample(100, nil)
	h.sample(100, nil)
	if len(store.events) != 1 || store.events[0].Threshold != 99 || h.queue.jobs[0].ChannelIDs[0] != "ch-9" {
		t.Fatalf("events=%v jobs=%+v", store.eventKinds(), h.queue.jobs)
	}
	// Disabling while firing clears the state silently.
	store.overrides[0].Disabled = true
	h.sample(100, nil)
	if len(store.events) != 1 || len(store.cleared) != 1 || h.queue.len() != 1 {
		t.Fatalf("events=%v cleared=%v jobs=%d", store.eventKinds(), store.cleared, h.queue.len())
	}
}

func TestAppMetricsUseHostMemoryAndNames(t *testing.T) {
	store := newStore()
	store.addRule(alert.Rule{TargetKind: alert.TargetApp, Metric: alert.MetricMemory, Threshold: 50, DurationMinutes: 1})
	h := newHarness(store)
	// No limit: 6 GB of the host's 8 GB = 75 %. The unknown app is ignored.
	h.sample(10, map[string]metrics.Values{app: {"mem_used": 6e9}, "ghost": {"mem_used": 8e9}})
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"fired:memory:app-1"}) {
		t.Fatalf("events = %v", got)
	}
	if ev := store.events[0]; ev.AppName != "nextcloud" || !strings.HasPrefix(ev.Title, "🔥 prod-1 / nextcloud · Memory 75%") ||
		!strings.Contains(ev.Message, "/server/srv-1/app/app-1?tab=metrics") {
		t.Fatalf("event = %+v", ev)
	}
}

func TestDownFiresAfterGraceAndResolvesOnEvent(t *testing.T) {
	store := newStore()
	store.addRule(alert.Rule{TargetKind: alert.TargetServer, Metric: alert.MetricDown, DurationMinutes: 5})
	h := newHarness(store)
	ctx := context.Background()
	h.ev.ObserveHeartbeat(ctx, srv)
	h.clock.t = t0.Add(4*time.Minute + 59*time.Second)
	h.ev.Sweep(ctx)
	if len(store.events) != 0 {
		t.Fatalf("fired early: %v", store.eventKinds())
	}
	h.clock.t = t0.Add(5 * time.Minute)
	h.ev.Sweep(ctx)
	h.ev.Sweep(ctx) // no repeat
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"fired:down:"}) || store.events[0].Title != "🔥 prod-1 is down for 5m" {
		t.Fatalf("events = %v (%q)", got, store.events[0].Title)
	}
	h.ev.ObserveAppStatus(ctx, srv, map[string]bool{app: false})
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"fired:down:", "resolved:down:"}) {
		t.Fatalf("events = %v", got)
	}
}

func TestDownWaitsForGraceOnShortRules(t *testing.T) {
	store := newStore()
	store.addRule(alert.Rule{TargetKind: alert.TargetServer, Metric: alert.MetricDown, DurationMinutes: 1})
	h := newHarness(store)
	h.ev.ObserveHeartbeat(context.Background(), srv)
	h.clock.t = t0.Add(80 * time.Second) // past 1 min, inside the 90 s grace
	h.ev.Sweep(context.Background())
	if len(store.events) != 0 {
		t.Fatalf("fired inside the liveness grace: %v", store.eventKinds())
	}
	h.clock.t = t0.Add(90 * time.Second)
	h.ev.Sweep(context.Background())
	if len(store.events) != 1 {
		t.Fatalf("events = %v", store.eventKinds())
	}
}

func TestDownCountsFromStartWhenNeverHeard(t *testing.T) {
	store := newStore()
	store.addRule(alert.Rule{TargetKind: alert.TargetServer, Metric: alert.MetricDown, DurationMinutes: 5})
	h := newHarness(store)
	h.clock.t = t0.Add(10 * time.Minute)
	if err := h.ev.Load(context.Background()); err != nil { // the API starts at t0+10m
		t.Fatal(err)
	}
	h.clock.t = t0.Add(14 * time.Minute)
	h.ev.Sweep(context.Background())
	if len(store.events) != 0 {
		t.Fatalf("fired from before the start: %v", store.eventKinds())
	}
	h.clock.t = t0.Add(15 * time.Minute)
	h.ev.Sweep(context.Background())
	if len(store.events) != 1 {
		t.Fatalf("events = %v", store.eventKinds())
	}
}

func TestAppDownFiresAfterDuration(t *testing.T) {
	store := newStore()
	store.addRule(alert.Rule{TargetKind: alert.TargetApp, Metric: alert.MetricDown, DurationMinutes: 5})
	h := newHarness(store)
	ctx := context.Background()
	h.ev.ObserveAppStatus(ctx, srv, map[string]bool{app: true})
	h.clock.t = t0.Add(3 * time.Minute)
	h.ev.ObserveAppStatus(ctx, srv, map[string]bool{app: true})
	if len(store.events) != 0 {
		t.Fatalf("fired early: %v", store.eventKinds())
	}
	h.clock.t = t0.Add(5 * time.Minute)
	h.ev.ObserveAppStatus(ctx, srv, map[string]bool{app: true})
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"fired:down:app-1"}) {
		t.Fatalf("events = %v", got)
	}
	// Stopped (not problematic) resolves; a fresh problem restarts the clock.
	h.clock.t = t0.Add(6 * time.Minute)
	h.ev.ObserveAppStatus(ctx, srv, map[string]bool{app: false})
	h.clock.t = t0.Add(7 * time.Minute)
	h.ev.ObserveAppStatus(ctx, srv, map[string]bool{app: true})
	h.clock.t = t0.Add(11 * time.Minute)
	h.ev.ObserveAppStatus(ctx, srv, map[string]bool{app: true})
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"fired:down:app-1", "resolved:down:app-1"}) {
		t.Fatalf("events = %v", got)
	}
}

func TestTwoEvaluatorsOneEvent(t *testing.T) {
	store := newStore()
	cpuRule(store, 90, 1)
	a, b := newHarness(store), newHarness(store)
	a.sample(95, nil)
	b.sample(95, nil)
	if len(store.events) != 1 || a.queue.len()+b.queue.len() != 1 {
		t.Fatalf("events=%d jobs=%d+%d", len(store.events), a.queue.len(), b.queue.len())
	}
	// Each replica tells its own browsers.
	if len(a.notices) != 1 || len(b.notices) != 1 {
		t.Fatalf("notices = %d, %d", len(a.notices), len(b.notices))
	}
}

func TestLoadRestoresFiringAndBuffers(t *testing.T) {
	store := newStore()
	rule := cpuRule(store, 90, 10)
	store.states[alert.Key{RuleID: rule.ID, Target: alert.Target{ServerID: srv}}] = alert.StateFiring
	var hist fakeHistory
	for i := 0; i < 9; i++ {
		at := t0.Add(time.Duration(i-9) * time.Minute)
		hist = append(hist,
			metrics.TargetBucket{ServerID: srv, Bucket: metrics.Bucket{Start: at, Samples: 1, Data: metrics.Values{"cpu": 95, "mem_total": 8e9}}},
			metrics.TargetBucket{ServerID: srv, AppID: app, Bucket: metrics.Bucket{Start: at, Samples: 1, Data: metrics.Values{"mem_used": 6e9}}},
		)
	}
	h := newHarness(store)
	h.ev.cfg.History = hist
	if err := h.ev.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.sample(95, nil) // already firing: no repeat
	if len(store.events) != 0 {
		t.Fatalf("re-fired after restart: %v", store.eventKinds())
	}
	if got := h.ev.buffers[alert.Target{ServerID: srv, AppID: app}]; got == nil || got.Len() != 9 {
		t.Fatalf("app buffer not refilled: %+v", got)
	}
	avg, ok := h.ev.buffers[alert.Target{ServerID: srv, AppID: app}].Average(alert.MetricMemory, 5*time.Minute)
	if !ok || avg != 75 {
		t.Fatalf("app memory from host total = %v, %v", avg, ok)
	}
	for i := 0; i < 8; i++ {
		h.sample(10, nil)
	}
	if got := store.eventKinds(); !reflect.DeepEqual(got, []string{"resolved:cpu:"}) {
		t.Fatalf("events = %v", got)
	}
}

func TestStoreErrorRetriesNextSample(t *testing.T) {
	store := newStore()
	cpuRule(store, 90, 1)
	h := newHarness(store)
	store.fail = errStore
	h.sample(95, nil)
	store.fail = nil
	h.sample(95, nil)
	if len(store.events) != 1 {
		t.Fatalf("events = %v", store.eventKinds())
	}
}

func TestUnknownTargetsAreIgnoredAndPruned(t *testing.T) {
	store := newStore()
	cpuRule(store, 90, 1)
	h := newHarness(store)
	h.ev.ObserveMetrics(context.Background(), "srv-ghost", t0, metrics.Values{"cpu": 99}, nil)
	if len(store.events) != 0 || h.ev.buffers[alert.Target{ServerID: "srv-ghost"}] != nil {
		t.Fatal("an unknown server was evaluated")
	}
	h.sample(10, map[string]metrics.Values{app: {"cpu": 1}})
	if h.ev.buffers[alert.Target{ServerID: srv, AppID: app}] == nil {
		t.Fatal("known app not buffered")
	}
	// The app is deleted; the next directory reload drops its buffer.
	store.mu.Lock()
	store.dir.Apps = map[string]alert.AppRef{}
	store.mu.Unlock()
	h.clock.t = h.clock.t.Add(directoryTTL)
	h.sample(10, nil)
	if h.ev.buffers[alert.Target{ServerID: srv, AppID: app}] != nil {
		t.Fatal("deleted app's buffer kept")
	}
}
