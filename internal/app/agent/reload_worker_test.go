package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/logger"
)

// fakeReloader stands in for the orchestrator. Candidates are "every app in
// stale that has not been reloaded since it went stale", like the real
// stale filter; block, when set, holds each reload until released or the
// reload's context ends. Apps in skip report skipped (no longer running);
// apps in stopped are left out of an app-id lookup (not running).
type fakeReloader struct {
	mu       sync.Mutex
	stale    map[string][]string
	reloads  []string
	failWith map[string]error
	skip     map[string]bool
	stopped  map[string]bool
	block    chan struct{}
	started  chan string
	// candBlock, when set, holds each ReloadCandidates call until closed or
	// its context ends; candGaveUp then receives the context error.
	candBlock  chan struct{}
	candGaveUp chan error
}

func (f *fakeReloader) ReloadCandidates(ctx context.Context, req command.ReloadVarsRequest) ([]string, error) {
	if f.candBlock != nil {
		select {
		case <-f.candBlock:
		case <-ctx.Done():
			f.candGaveUp <- ctx.Err()
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(req.AppIDs) > 0 {
		var out []string
		for _, id := range req.AppIDs {
			if !f.stopped[id] {
				out = append(out, id)
			}
		}
		return out, nil
	}
	var out []string
	for _, id := range []string{"a", "b", "c"} {
		if _, ok := f.stale[id]; ok {
			out = append(out, id)
		}
	}
	return out, nil
}

func (f *fakeReloader) ReloadSharedVars(ctx context.Context, appID string) ([]string, bool, error) {
	if f.started != nil {
		f.started <- appID
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reloads = append(f.reloads, appID)
	names := f.stale[appID]
	if f.skip[appID] {
		return names, true, nil
	}
	delete(f.stale, appID)
	return names, false, f.failWith[appID]
}

func (f *fakeReloader) reloaded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reloads...)
}

func testLog() *logger.Logger {
	return logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
}

func awaitOutcomes(t *testing.T, ch <-chan sinkCall, n int) []command.VarsReloadedPayload {
	t.Helper()
	var out []command.VarsReloadedPayload
	for len(out) < n {
		select {
		case c := <-ch:
			if c.kind != bus.EventVarsReloaded {
				t.Fatalf("kind = %q", c.kind)
			}
			var p command.VarsReloadedPayload
			if err := json.Unmarshal(c.payload, &p); err != nil {
				t.Fatal(err)
			}
			out = append(out, p)
		case <-time.After(3 * time.Second):
			t.Fatalf("got %d of %d outcomes", len(out), n)
		}
	}
	return out
}

func TestReloadWorkerReloadsAndReportsEachApp(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"SMTP_PASS"}, "b": {"S3_KEY"}},
		failWith: map[string]error{"b": errors.New("compose up failed")}}
	got := make(chan sinkCall, 8)
	w := NewReloadWorker(f, collectSink(got), testLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	w.Enqueue(command.ReloadVarsRequest{})
	outs := awaitOutcomes(t, got, 2)
	if outs[0].AppID != "a" || !outs[0].OK || outs[0].Names[0] != "SMTP_PASS" || outs[0].ServerID != "" {
		t.Fatalf("a = %+v", outs[0])
	}
	if outs[1].AppID != "b" || outs[1].OK || !strings.Contains(outs[1].Error, "S3_KEY") || !strings.Contains(outs[1].Error, "compose up failed") {
		t.Fatalf("b = %+v", outs[1])
	}
}

func TestReloadWorkerSkippedAppReportsNothing(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"X"}, "b": {"Y"}}, skip: map[string]bool{"a": true}}
	got := make(chan sinkCall, 8)
	w := NewReloadWorker(f, collectSink(got), testLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	w.Enqueue(command.ReloadVarsRequest{})
	outs := awaitOutcomes(t, got, 1)
	if outs[0].AppID != "b" || !outs[0].OK {
		t.Fatalf("outcome = %+v, the skipped app must not report", outs[0])
	}
	time.Sleep(50 * time.Millisecond)
	select {
	case c := <-got:
		t.Fatalf("unexpected outcome %s", c.payload)
	default:
	}
	if r := f.reloaded(); len(r) != 2 {
		t.Fatalf("reloads = %v, want both attempted", r)
	}
}

func TestReloadWorkerCoalescesWorkArrivingMidPass(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"X"}, "b": {"X"}},
		block: make(chan struct{}), started: make(chan string, 8)}
	got := make(chan sinkCall, 8)
	w := NewReloadWorker(f, collectSink(got), testLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	w.Enqueue(command.ReloadVarsRequest{})
	if first := <-f.started; first != "a" {
		t.Fatalf("first = %s", first)
	}
	// While a reloads: the same change is pushed again, plus a request
	// naming b explicitly.
	w.Enqueue(command.ReloadVarsRequest{})
	w.Enqueue(command.ReloadVarsRequest{Names: []string{"X"}})
	w.Enqueue(command.ReloadVarsRequest{AppIDs: []string{"b"}})
	close(f.block)
	awaitOutcomes(t, got, 2)
	time.Sleep(50 * time.Millisecond) // nothing else may follow
	if r := f.reloaded(); len(r) != 2 || r[0] != "a" || r[1] != "b" {
		t.Fatalf("reloads = %v, want [a b] once each", r)
	}
}

func TestReloadWorkerDeadline(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"X"}}, block: make(chan struct{})}
	got := make(chan sinkCall, 2)
	w := NewReloadWorker(f, collectSink(got), testLog())
	w.timeout = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	w.Enqueue(command.ReloadVarsRequest{})
	out := awaitOutcomes(t, got, 1)[0]
	if out.OK || !strings.Contains(out.Error, "deadline") {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestReloadWorkerCandidateLookupDeadline(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"X"}},
		candBlock: make(chan struct{}), candGaveUp: make(chan error, 4)}
	got := make(chan sinkCall, 4)
	w := NewReloadWorker(f, collectSink(got), testLog())
	w.candidatesTimeout = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	// A hung docker ps: the lookup gives up at its deadline, the pass ends
	// without reloading anything.
	w.Enqueue(command.ReloadVarsRequest{})
	select {
	case err := <-f.candGaveUp:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("lookup ended with %v, want its deadline", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("candidate lookup never gave up")
	}
	if r := f.reloaded(); len(r) != 0 {
		t.Fatalf("reloads = %v", r)
	}

	// Docker recovers: the worker still serves the next request.
	close(f.candBlock)
	w.Enqueue(command.ReloadVarsRequest{})
	if out := awaitOutcomes(t, got, 1)[0]; out.AppID != "a" || !out.OK {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestReloadWorkerForgetDropsQueuedApp(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"X"}, "b": {"X"}},
		block: make(chan struct{}), started: make(chan string, 8)}
	got := make(chan sinkCall, 8)
	w := NewReloadWorker(f, collectSink(got), testLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	w.Enqueue(command.ReloadVarsRequest{})
	<-f.started // a is reloading, b is queued
	w.Forget("b")
	close(f.block)
	awaitOutcomes(t, got, 1)
	time.Sleep(50 * time.Millisecond)
	if r := f.reloaded(); len(r) != 1 || r[0] != "a" {
		t.Fatalf("reloads = %v, the deleted app must be dropped", r)
	}
	w.mu.Lock()
	left := len(w.dropped)
	w.mu.Unlock()
	if left != 0 {
		t.Fatalf("dropped = %d entries after the pass, want none", left)
	}
}

func TestReloadWorkerForgetWhileIdleLeavesNothing(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{}}
	got := make(chan sinkCall, 4)
	w := NewReloadWorker(f, collectSink(got), testLog())
	// Not running: an explicit request is queued, then the app is deleted.
	w.Enqueue(command.ReloadVarsRequest{AppIDs: []string{"a"}})
	w.Forget("a")
	w.Forget("never-queued")
	w.mu.Lock()
	dropped, queued := len(w.dropped), len(w.pending.apps)
	w.mu.Unlock()
	if dropped != 0 || queued != 0 {
		t.Fatalf("dropped = %d, queued = %d; an idle Forget must leave nothing behind", dropped, queued)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	time.Sleep(50 * time.Millisecond)
	if r := f.reloaded(); len(r) != 0 {
		t.Fatalf("reloads = %v, the forgotten app must not reload", r)
	}
}

func TestReloadWorkerExitsOnCancel(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"X"}, "b": {"X"}},
		block: make(chan struct{}), started: make(chan string, 8)}
	w := NewReloadWorker(f, func(bus.EventKind, []byte) error { return nil }, testLog())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	w.Enqueue(command.ReloadVarsRequest{})
	<-f.started
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not exit on cancel")
	}
	if r := f.reloaded(); len(r) != 0 {
		t.Fatalf("reloads after cancel = %v", r)
	}
}

// awaitReload is the outcome a waited app's channel delivers.
func awaitReload(t *testing.T, ch <-chan ReloadOutcome) ReloadOutcome {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(3 * time.Second):
		t.Fatal("no outcome for the waited app")
		return ReloadOutcome{}
	}
}

func TestReloadWorkerEnqueueAppDeliversTheOutcome(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"SMTP_PASS"}, "b": {"S3_KEY"}, "c": {"X"}},
		failWith: map[string]error{"b": errors.New("compose up failed")},
		skip:     map[string]bool{"c": true}, stopped: map[string]bool{"d": true}}
	got := make(chan sinkCall, 8)
	w := NewReloadWorker(f, collectSink(got), testLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	if o := awaitReload(t, w.EnqueueApp("a")); o.Err != nil || o.Skipped || len(o.Names) != 1 || o.Names[0] != "SMTP_PASS" {
		t.Fatalf("a = %+v", o)
	}
	if o := awaitReload(t, w.EnqueueApp("b")); o.Err == nil || !strings.Contains(o.Err.Error(), "S3_KEY") || !strings.Contains(o.Err.Error(), "compose up failed") {
		t.Fatalf("b = %+v", o)
	}
	// Stopped when its turn came (c), or not running at the lookup (d).
	if o := awaitReload(t, w.EnqueueApp("c")); o.Err != nil || !o.Skipped {
		t.Fatalf("c = %+v", o)
	}
	if o := awaitReload(t, w.EnqueueApp("d")); o.Err != nil || !o.Skipped {
		t.Fatalf("d = %+v", o)
	}
	// The reloads still report as vars.reloaded events (a and b; c and d
	// were skipped).
	if outs := awaitOutcomes(t, got, 2); outs[0].AppID != "a" || outs[1].AppID != "b" {
		t.Fatalf("events = %+v", outs)
	}
	w.mu.Lock()
	left := len(w.waiters)
	w.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d waiters left behind", left)
	}
}

func TestReloadWorkerWaitDuringAReloadTakesTheNextOne(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"X"}}, block: make(chan struct{}), started: make(chan string, 4)}
	w := NewReloadWorker(f, func(bus.EventKind, []byte) error { return nil }, testLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	first := w.EnqueueApp("a")
	<-f.started
	// A change arrives while a reloads: its wait is for a reload that
	// renders the newer snapshot, not the one in flight.
	second := w.EnqueueApp("a")
	f.block <- struct{}{}
	awaitReload(t, first)
	select {
	case o := <-second:
		t.Fatalf("the second wait took the first reload's outcome: %+v", o)
	case <-f.started:
	}
	f.block <- struct{}{}
	awaitReload(t, second)
	if r := f.reloaded(); len(r) != 2 {
		t.Fatalf("reloads = %v, want two", r)
	}
}

func TestReloadWorkerEndsWaitsOnForgetFailureAndStop(t *testing.T) {
	// Deleted before its turn.
	w := NewReloadWorker(&fakeReloader{}, func(bus.EventKind, []byte) error { return nil }, testLog())
	ch := w.EnqueueApp("a")
	w.Forget("a")
	if o := awaitReload(t, ch); !errors.Is(o.Err, errAppDeleted) {
		t.Fatalf("forgotten = %+v", o)
	}

	// The lookup fails (a hung docker ps).
	f := &fakeReloader{candBlock: make(chan struct{}), candGaveUp: make(chan error, 4)}
	w = NewReloadWorker(f, func(bus.EventKind, []byte) error { return nil }, testLog())
	w.candidatesTimeout = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	if o := awaitReload(t, w.EnqueueApp("a")); !errors.Is(o.Err, context.DeadlineExceeded) {
		t.Fatalf("failed lookup = %+v", o)
	}

	// The worker stops while one waits behind a reload in flight.
	close(f.candBlock)
	f.stale = map[string][]string{"b": {"X"}}
	f.block, f.started = make(chan struct{}), make(chan string, 4)
	w.Enqueue(command.ReloadVarsRequest{AppIDs: []string{"b"}})
	<-f.started
	waiting := w.EnqueueApp("c")
	cancel()
	if o := awaitReload(t, waiting); !errors.Is(o.Err, context.Canceled) {
		t.Fatalf("stopped = %+v", o)
	}
}
