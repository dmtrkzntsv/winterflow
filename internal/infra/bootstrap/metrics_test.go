package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/metrics"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/transport/bus"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

type insertCall struct {
	serverID string
	at       time.Time
	server   metrics.Values
	apps     map[string]metrics.Values
}

type fakeMetricsStore struct {
	mu       sync.Mutex
	calls    []insertCall
	err      error
	deadline bool
}

func (f *fakeMetricsStore) InsertSample(ctx context.Context, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, f.deadline = ctx.Deadline()
	f.calls = append(f.calls, insertCall{serverID, at, server, apps})
	return f.err
}

func metricsEvent(t *testing.T, s metrics.Sample) bus.EventMessage {
	t.Helper()
	body, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return bus.EventMessage{ServerID: "srv-1", Kind: bus.EventServerMetrics, Payload: body}
}

func TestMetricsEventIsStoredWithoutFanOut(t *testing.T) {
	rec := &recorder{}
	sink, cache := newTestSink(rec, []string{"alice"})
	store := &fakeMetricsStore{}
	sink.metrics, sink.latest = store, metrics.NewLatestStore()
	cpu := 42.0
	ts := time.Now().Add(-30 * time.Second).Unix()
	sink.handle(context.Background(), metricsEvent(t, metrics.Sample{
		TS:     ts,
		Server: metrics.ServerSample{CPU: &cpu, MemTotal: 8},
		Apps:   []metrics.AppSample{{AppID: "app-1", CPU: &cpu}, {AppID: ""}},
	}))

	if len(store.calls) != 1 {
		t.Fatalf("inserts = %d", len(store.calls))
	}
	c := store.calls[0]
	if c.serverID != "srv-1" || c.at.Unix() != ts || c.server["cpu"] != 42 || len(c.apps) != 1 || c.apps["app-1"]["cpu"] != 42 {
		t.Fatalf("insert = %+v", c)
	}
	if !store.deadline {
		t.Fatal("InsertSample ran without a deadline")
	}
	l, ok := sink.latest.Get("srv-1")
	if !ok || l.Server.MemTotal != 8 || l.At.Unix() != ts {
		t.Fatalf("latest = %+v, %v", l, ok)
	}
	// Metrics are pulled by the UI, never pushed, and are not a heartbeat.
	if rec.count() != 0 {
		t.Fatalf("metrics fanned out %d notifications", rec.count())
	}
	if cache.ServerLiveness("srv-1", time.Now()) != "unknown" {
		t.Fatal("a metrics event changed liveness")
	}
}

func TestMetricsEventDroppedWhenDisabledOrUndecodable(t *testing.T) {
	sink, _ := newTestSink(&recorder{}, nil)
	sink.handle(context.Background(), metricsEvent(t, metrics.Sample{})) // metrics nil: dropped

	store := &fakeMetricsStore{err: errors.New("db down")}
	sink.metrics, sink.latest = store, metrics.NewLatestStore()
	sink.handle(context.Background(), bus.EventMessage{ServerID: "srv-1", Kind: bus.EventServerMetrics, Payload: []byte("{")})
	if len(store.calls) != 0 {
		t.Fatal("undecodable event stored")
	}
	// A failed write still updates the dashboard's latest sample.
	sink.handle(context.Background(), metricsEvent(t, metrics.Sample{}))
	if _, ok := sink.latest.Get("srv-1"); !ok {
		t.Fatal("latest not kept after a failed write")
	}
}

type fakeMaintainer struct {
	rollups, prunes int
	fail            bool
	deadlines       int
}

func (f *fakeMaintainer) Rollup(ctx context.Context, _ time.Time) (int, error) {
	f.rollups++
	if _, ok := ctx.Deadline(); ok {
		f.deadlines++
	}
	if f.fail {
		return 0, errors.New("locked")
	}
	return 1, nil
}

func (f *fakeMaintainer) Prune(ctx context.Context, _ time.Time) (int64, error) {
	f.prunes++
	if _, ok := ctx.Deadline(); ok {
		f.deadlines++
	}
	if f.fail {
		return 0, errors.New("locked")
	}
	return 1, nil
}

func TestMetricsMaintenancePrunesHourly(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	repo := &fakeMaintainer{}
	latest := metrics.NewLatestStore()
	m := &metricsMaintenance{repo: repo, latest: latest, log: log}
	start := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	latest.Set("gone", metrics.Latest{At: start.Add(-2 * time.Hour)})
	for i := 0; i < 7; i++ { // 00:00 .. 01:00 every 10 minutes
		m.tick(context.Background(), start.Add(time.Duration(i)*metricsMaintenanceInterval))
	}
	if repo.rollups != 7 || repo.prunes != 2 {
		t.Fatalf("rollups = %d prunes = %d, want 7 and 2 (00:00, 01:00)", repo.rollups, repo.prunes)
	}
	if repo.deadlines != repo.rollups+repo.prunes {
		t.Fatalf("%d of %d DB calls had a deadline", repo.deadlines, repo.rollups+repo.prunes)
	}
	if _, ok := latest.Get("gone"); ok {
		t.Fatal("stale latest sample kept")
	}
	// A failed prune is retried on the next tick, not an hour later.
	repo.fail = true
	m.tick(context.Background(), start.Add(2*time.Hour))
	repo.fail = false
	m.tick(context.Background(), start.Add(2*time.Hour+metricsMaintenanceInterval))
	if repo.prunes != 4 {
		t.Fatalf("prunes = %d, want a retry after the failure", repo.prunes)
	}
}

func TestRunMetricsMaintenanceStopsOnCancel(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runMetricsMaintenance(ctx, &fakeMaintainer{}, metrics.NewLatestStore(), time.Millisecond, log)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance loop did not stop")
	}
}

// End to end through WireCore: a server.metrics event on the bus becomes a
// tier-1 row the chart query returns.
func TestWireCoreStoresMetricsEvents(t *testing.T) {
	t.Setenv("DATABASE_URL", "sqlite://"+filepath.Join(t.TempDir(), "wf.sqlite"))
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := db.Open(log, cfg.GetDbURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Shutdown() })
	b := membus.NewBus(log)
	deps, _ := WireCore(ctx, b, conn, cfg, log)
	if deps.MetricRepository == nil || deps.LatestMetrics == nil {
		t.Fatal("metrics deps not wired")
	}
	srv := dbtest.ID(11)
	dbtest.SeedServer(t, conn, dbtest.ID(1), srv)

	cpu := 7.0
	body, _ := json.Marshal(metrics.Sample{TS: time.Now().Unix(), Server: metrics.ServerSample{CPU: &cpu}})
	if err := b.Publish(ctx, cfg.GetBusEventsQueue(), bus.EventMessage{ServerID: srv, Kind: bus.EventServerMetrics, Payload: body}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := deps.MetricRepository.Series(ctx, srv, "", metrics.Tier1, time.Now().Add(-time.Hour))
		if err == nil && len(got) == 1 && got[0].Data["cpu"] == 7 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sample not stored: %+v, %v", got, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
