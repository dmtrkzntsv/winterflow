package agent

import (
	"context"
	"encoding/json"
	"errors"
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
	go RunStatusReporter(ctx, src, collectSink(got), 30*time.Millisecond, log)

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
	go RunStatusReporter(ctx, src, collectSink(got), 20*time.Millisecond, log)

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
		RunStatusReporter(ctx, src, collectSink(got), 10*time.Millisecond, log)
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
