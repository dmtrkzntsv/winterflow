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
// reload's context ends. Apps in skip report skipped (no longer running).
type fakeReloader struct {
	mu       sync.Mutex
	stale    map[string][]string
	reloads  []string
	failWith map[string]error
	skip     map[string]bool
	block    chan struct{}
	started  chan string
}

func (f *fakeReloader) ReloadCandidates(_ context.Context, req command.ReloadVarsRequest) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(req.AppIDs) > 0 {
		return append([]string(nil), req.AppIDs...), nil
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
