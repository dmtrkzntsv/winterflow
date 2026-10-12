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

type fakeStatusSource struct {
	mu    sync.Mutex
	calls int
	fail  bool
}

func (f *fakeStatusSource) GetAppsStatus(context.Context) ([]command.AppStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail {
		return nil, errors.New("docker down")
	}
	return []command.AppStatus{{AppID: "app-1", StatusCode: command.ContainerStatusActive}}, nil
}

func (f *fakeStatusSource) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type sinkCall struct {
	kind    bus.EventKind
	payload []byte
}

func collectSink(ch chan<- sinkCall) EventSink {
	return func(kind bus.EventKind, payload []byte) error {
		ch <- sinkCall{kind: kind, payload: payload}
		return nil
	}
}

func TestStatusReporterSendsImmediatelyAndOnTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})

	src := &fakeStatusSource{}
	got := make(chan sinkCall, 16)
	go RunStatusReporter(ctx, src, collectSink(got), 30*time.Millisecond, nil, log)

	// Immediate first report.
	select {
	case call := <-got:
		if call.kind != bus.EventAppsStatus {
			t.Fatalf("kind = %q", call.kind)
		}
		var body command.GetAppsStatusResponse
		if err := json.Unmarshal(call.payload, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Apps) != 1 || body.Apps[0].AppID != "app-1" {
			t.Fatalf("payload = %+v", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no immediate report")
	}

	// At least one ticker-driven report.
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no ticker report")
	}
}

func TestStatusReporterSurvivesSourceErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})

	src := &fakeStatusSource{fail: true}
	got := make(chan sinkCall, 16)
	go RunStatusReporter(ctx, src, collectSink(got), 20*time.Millisecond, nil, log)

	// Give it several ticks to prove the loop keeps polling despite errors.
	time.Sleep(120 * time.Millisecond)
	if src.callCount() < 2 {
		t.Fatalf("reporter stopped polling after error: %d calls", src.callCount())
	}
	select {
	case call := <-got:
		t.Fatalf("no event should be sent on source error, got %+v", call)
	default:
	}
}

func TestStatusReporterStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})

	src := &fakeStatusSource{}
	got := make(chan sinkCall, 16)
	done := make(chan struct{})
	go func() {
		RunStatusReporter(ctx, src, collectSink(got), 10*time.Millisecond, nil, log)
		close(done)
	}()

	<-got // wait for the first report
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reporter did not stop on cancel")
	}
}

// revisionSource embeds a pointer so the fake's mutex is never copied.
type revisionSource struct{ *fakeStatusSource }

func (revisionSource) VarsRevision() int64 { return 9 }

func TestStatusReporterAddsVarsRevision(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan sinkCall, 4)
	go RunStatusReporter(ctx, revisionSource{&fakeStatusSource{}}, collectSink(got), time.Hour, nil, testLog())
	call := <-got
	var body command.GetAppsStatusResponse
	if err := json.Unmarshal(call.payload, &body); err != nil || body.VarsRevision == nil || *body.VarsRevision != 9 {
		t.Fatalf("report = %s", call.payload)
	}
	// Wrapped in the snapshot the metrics reporter shares, it still does.
	wrapped := make(chan sinkCall, 4)
	go RunStatusReporter(ctx, NewStatusSnapshot(revisionSource{&fakeStatusSource{}}), collectSink(wrapped), time.Hour, nil, testLog())
	if c := <-wrapped; !strings.Contains(string(c.payload), `"vars_revision":9`) {
		t.Fatalf("wrapped report = %s", c.payload)
	}
	// Inside the backup-health decoration too (the production wiring).
	decorated := make(chan sinkCall, 4)
	go RunStatusReporter(ctx, NewStatusSnapshot(WithBackupHealth(revisionSource{&fakeStatusSource{}}, noBackups{})), collectSink(decorated), time.Hour, nil, testLog())
	if c := <-decorated; !strings.Contains(string(c.payload), `"vars_revision":9`) {
		t.Fatalf("decorated report = %s", c.payload)
	}
	// A source without revisions (an old orchestrator) sends none.
	plain := make(chan sinkCall, 4)
	go RunStatusReporter(ctx, &fakeStatusSource{}, collectSink(plain), time.Hour, nil, testLog())
	if c := <-plain; strings.Contains(string(c.payload), "vars_revision") {
		t.Fatalf("report = %s", c.payload)
	}
}

// A nudge makes the reporter publish at once, long before its tick.
func TestStatusReporterReportsOnNudge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	got := make(chan sinkCall, 16)
	nudge := make(chan struct{}, 1)
	go RunStatusReporter(ctx, &fakeStatusSource{}, collectSink(got), time.Hour, nudge, log)
	select {
	case <-got: // the immediate first report
	case <-time.After(2 * time.Second):
		t.Fatal("no immediate report")
	}
	nudge <- struct{}{}
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("a nudge did not trigger a report")
	}
	select {
	case call := <-got:
		t.Fatalf("an extra report: %+v", call)
	case <-time.After(100 * time.Millisecond):
	}
}
