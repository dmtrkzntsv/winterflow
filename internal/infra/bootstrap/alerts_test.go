package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/service/alerting"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/transport/bus"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
	"winterflow/pkg/secretbox"
)

// fakeObserver records what the sink tells the alert evaluator.
type fakeObserver struct {
	mu    sync.Mutex
	calls []string
	apps  map[string]bool
	vals  map[string]metrics.Values
}

func (o *fakeObserver) add(c string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, c)
}

func (o *fakeObserver) ObserveHeartbeat(_ context.Context, id string) { o.add("heartbeat:" + id) }
func (o *fakeObserver) ObserveMetrics(_ context.Context, id string, _ time.Time, _ metrics.Values, apps map[string]metrics.Values) {
	o.mu.Lock()
	o.vals = apps
	o.mu.Unlock()
	o.add("metrics:" + id)
}
func (o *fakeObserver) ObserveAppStatus(_ context.Context, id string, problematic map[string]bool) {
	o.mu.Lock()
	o.apps = problematic
	o.mu.Unlock()
	o.add("status:" + id)
}
func (o *fakeObserver) Sweep(context.Context) { o.add("sweep") }

func TestSinkDrivesTheAlertEvaluator(t *testing.T) {
	rec := &recorder{}
	sink, cache := newTestSink(rec, []string{"alice"})
	obs := &fakeObserver{}
	sink.alerts = obs
	sink.metrics = &fakeMetricsStore{}
	ctx := context.Background()

	sink.handle(ctx, bus.EventMessage{ServerID: "srv-1", Kind: bus.EventServerOnline})
	caps, _ := json.Marshal(capabilitiesEvent{})
	sink.handle(ctx, bus.EventMessage{ServerID: "srv-1", Kind: bus.EventCapabilities, Payload: caps})
	status, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{
		{AppID: "a1", StatusCode: command.ContainerStatusProblematic},
		{AppID: "a2", StatusCode: command.ContainerStatusStopped},
	}})
	sink.handle(ctx, bus.EventMessage{ServerID: "srv-1", Kind: bus.EventAppsStatus, Payload: status})
	sink.handle(ctx, bus.EventMessage{ServerID: "srv-1", Kind: bus.EventAppsStatus, Payload: status}) // unchanged: still observed
	cpu := 5.0
	sample, _ := json.Marshal(metrics.Sample{TS: time.Now().Unix(), Server: metrics.ServerSample{CPU: &cpu},
		Apps: []metrics.AppSample{{AppID: "a1", CPU: &cpu}}})
	sink.handle(ctx, bus.EventMessage{ServerID: "srv-1", Kind: bus.EventServerMetrics, Payload: sample})
	sink.sweep(ctx, cache, time.Now())

	want := []string{"heartbeat:srv-1", "heartbeat:srv-1", "status:srv-1", "status:srv-1", "metrics:srv-1", "sweep"}
	if !reflect.DeepEqual(obs.calls, want) {
		t.Fatalf("calls = %v", obs.calls)
	}
	if !reflect.DeepEqual(obs.apps, map[string]bool{"a1": true, "a2": false}) || obs.vals["a1"]["cpu"] != 5 {
		t.Fatalf("apps=%v vals=%v", obs.apps, obs.vals)
	}
}

func TestSinkWithMetricsOffStillHearsTheServer(t *testing.T) {
	sink, _ := newTestSink(&recorder{}, nil)
	obs := &fakeObserver{}
	sink.alerts = obs
	sink.handle(context.Background(), bus.EventMessage{ServerID: "srv-1", Kind: bus.EventServerMetrics, Payload: []byte(`{}`)})
	if !reflect.DeepEqual(obs.calls, []string{"heartbeat:srv-1"}) {
		t.Fatalf("calls = %v", obs.calls)
	}
}

// fakeAlertMaintainer counts maintenance calls.
type fakeAlertMaintainer struct {
	seeds, prunes int
	err           error
}

func (m *fakeAlertMaintainer) SeedDefaults(context.Context) (int, error) { m.seeds++; return 1, m.err }
func (m *fakeAlertMaintainer) PruneEvents(context.Context, time.Time) (int64, error) {
	m.prunes++
	return 3, m.err
}

func TestAlertMaintenanceTickSeedsAndPrunes(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	m := &fakeAlertMaintainer{}
	alertMaintenanceTick(context.Background(), m, time.Now(), log)
	m.err = errors.New("db down") // logged, never fatal
	alertMaintenanceTick(context.Background(), m, time.Now(), log)
	if m.seeds != 2 || m.prunes != 2 {
		t.Fatalf("seeds=%d prunes=%d", m.seeds, m.prunes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { runAlertMaintenance(ctx, m, time.Hour, log); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("maintenance loop ignored cancel")
	}
}

// End to end through WireCore: the default rules are seeded at startup and
// the alert deps are wired; without JWT_SECRET there is no channel box.
func TestWireCoreWiresAlerts(t *testing.T) {
	t.Setenv("DATABASE_URL", "sqlite://"+filepath.Join(t.TempDir(), "wf.sqlite"))
	t.Setenv("JWT_SECRET", "")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := db.Open(log, cfg.GetDbURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Shutdown() })
	deps, _ := WireCore(ctx, membus.NewBus(log), conn, cfg, log)
	if deps.AlertRepository == nil || deps.AlertNotifier == nil || deps.ChannelBox != nil {
		t.Fatalf("alert deps: repo=%v notifier=%v box=%v", deps.AlertRepository, deps.AlertNotifier, deps.ChannelBox)
	}
}

// syncBuffer is a log sink safe for the goroutines WireCore starts.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func wireCoreForTest(t *testing.T, log *logger.Logger) *Deps {
	t.Helper()
	t.Setenv("DATABASE_URL", "sqlite://"+filepath.Join(t.TempDir(), "wf.sqlite"))
	cfg := config.NewServerConfig("standalone")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	conn, err := db.Open(log, cfg.GetDbURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Shutdown() })
	deps, _ := WireCore(ctx, membus.NewBus(log), conn, cfg, log)
	return deps
}

// The alert warm start reads before the metrics maintenance starts, so
// the first rollup cannot hold SQLite's connection during Load.
func TestWireCoreWarmsAlertsBeforeMetricsMaintenance(t *testing.T) {
	t.Setenv("METRICS_ENABLED", "true")
	var mu sync.Mutex
	var order []string
	record := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, s)
	}
	prevLoad, prevStart := loadEvaluator, startMetricsMaintenance
	t.Cleanup(func() { loadEvaluator, startMetricsMaintenance = prevLoad, prevStart })
	loadEvaluator = func(e *alerting.Evaluator, ctx context.Context) error {
		record("load")
		return prevLoad(e, ctx)
	}
	startMetricsMaintenance = func(context.Context, metricsMaintainer, *metrics.LatestStore, *logger.Logger) {
		record("metrics")
	}
	wireCoreForTest(t, logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(order, []string{"load", "metrics"}) {
		t.Fatalf("order = %v", order)
	}
}

// The channel-box warning carries the actual error.
func TestWireCoreLogsChannelBoxError(t *testing.T) {
	t.Setenv("JWT_SECRET", "")
	var out syncBuffer
	log := &logger.Logger{Logger: slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelWarn}))}
	wireCoreForTest(t, log)
	if got := out.String(); !strings.Contains(got, secretbox.ErrNoSecret.Error()) {
		t.Fatalf("warning lacks the error: %s", got)
	}
}
