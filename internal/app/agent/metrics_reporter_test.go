package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/metrics"
	"winterflow/internal/infra/sysmetrics"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func fp(x float64) *float64 { return &x }

type fakeHost struct{ calls int }

func (h *fakeHost) Collect(time.Time) metrics.ServerSample {
	h.calls++
	return metrics.ServerSample{MemUsed: 1, MemTotal: 2}
}

type fakeContainers struct {
	mu    sync.Mutex
	stats map[string]sysmetrics.ContainerStat
	asked [][]string
}

func (c *fakeContainers) Collect(_ time.Time, ids []string) map[string]sysmetrics.ContainerStat {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, ids)
	return c.stats
}

func ctr(id string, code command.ContainerStatusCode) command.ContainerStatus {
	return command.ContainerStatus{ContainerID: id, StatusCode: code}
}

func TestBuildSampleSumsContainersPerApp(t *testing.T) {
	apps := []command.AppStatus{
		{AppID: "web", Containers: []command.ContainerStatus{
			ctr("w1", command.ContainerStatusActive), ctr("w2", command.ContainerStatusActive),
			ctr("w3", command.ContainerStatusStopped), // no cgroup, not counted
		}},
		{AppID: "nolimit", Containers: []command.ContainerStatus{
			ctr("n1", command.ContainerStatusActive), ctr("n2", command.ContainerStatusActive),
		}},
		{AppID: "blind", Containers: []command.ContainerStatus{ctr("b1", command.ContainerStatusActive)}},
		{AppID: "stopped", Containers: []command.ContainerStatus{ctr("s1", command.ContainerStatusStopped)}},
	}
	stats := map[string]sysmetrics.ContainerStat{
		"w1": {CPU: fp(10), MemUsed: 100, MemLimit: fp(1000), NetRx: fp(5), NetTx: fp(6)},
		"w2": {CPU: fp(2.5), MemUsed: 50, MemLimit: fp(500)}, // host network: no net
		"n1": {MemUsed: 10, MemLimit: fp(64)},                // first reading: no cpu
		"n2": {MemUsed: 20},                                  // unlimited
	}
	scan := &metrics.DiskScan{
		Docker: metrics.DockerDF{Images: 9},
		Apps:   map[string]metrics.AppDisk{"web": {Disk: 300, Image: 40}},
	}
	s := buildSample(time.Unix(1000, 0), metrics.ServerSample{}, apps, stats, scan)
	if s.TS != 1000 || s.Server.DockerDF == nil || s.Server.DockerDF.Images != 9 {
		t.Fatalf("sample = %+v", s)
	}
	by := map[string]metrics.AppSample{}
	for _, a := range s.Apps {
		by[a.AppID] = a
	}
	web := by["web"]
	if *web.CPU != 12.5 || *web.MemUsed != 150 || *web.MemLimit != 1500 || *web.NetRxBps != 5 || *web.NetTxBps != 6 {
		t.Fatalf("web = %+v", web)
	}
	if *web.Disk != 300 || *web.Image != 40 {
		t.Fatalf("web disk = %v image = %v", *web.Disk, *web.Image)
	}
	nl := by["nolimit"]
	if nl.CPU != nil || *nl.MemUsed != 30 || nl.MemLimit != nil || nl.NetRxBps != nil {
		t.Fatalf("nolimit = %+v (one unlimited container means no app limit)", nl)
	}
	if b := by["blind"]; !b.ContainersUnavailable || b.MemUsed != nil {
		t.Fatalf("blind = %+v", b)
	}
	if st := by["stopped"]; st.ContainersUnavailable || st.MemUsed != nil || st.Disk != nil {
		t.Fatalf("stopped app = %+v", st)
	}
}

func TestBuildSampleWithoutScan(t *testing.T) {
	s := buildSample(time.Unix(1, 0), metrics.ServerSample{}, []command.AppStatus{{AppID: "a"}}, nil, nil)
	if s.Server.DockerDF != nil || s.Apps[0].Disk != nil {
		t.Fatalf("no scan yet must mean no disk fields: %+v", s)
	}
}

type fakeStatus struct {
	apps []command.AppStatus
	err  error
}

func (f *fakeStatus) GetAppsStatus(context.Context) ([]command.AppStatus, error) {
	return f.apps, f.err
}

func TestStatusSnapshotKeepsLastGoodAnswer(t *testing.T) {
	src := &fakeStatus{}
	snap := NewStatusSnapshot(src)
	if _, ok := snap.Latest(); ok {
		t.Fatal("snapshot before the first poll")
	}
	src.apps = []command.AppStatus{{AppID: "a"}}
	if _, err := snap.GetAppsStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	src.err = errors.New("docker down")
	if _, err := snap.GetAppsStatus(context.Background()); err == nil {
		t.Fatal("error swallowed")
	}
	if apps, ok := snap.Latest(); !ok || len(apps) != 1 {
		t.Fatalf("latest = %v %v", apps, ok)
	}
}

func TestMetricsReporterSampleReadsOnlyRunningContainers(t *testing.T) {
	snap := NewStatusSnapshot(&fakeStatus{apps: []command.AppStatus{{AppID: "a", Containers: []command.ContainerStatus{
		ctr("c1", command.ContainerStatusActive), ctr("c2", command.ContainerStatusRestarting), ctr("c3", command.ContainerStatusStopped),
	}}}})
	if _, err := snap.GetAppsStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	host, cs := &fakeHost{}, &fakeContainers{}
	m := NewMetricsReporter(host, cs, snap)
	m.Sample(time.Unix(1, 0))
	if len(cs.asked) != 1 || len(cs.asked[0]) != 2 || cs.asked[0][0] != "c1" || cs.asked[0][1] != "c2" {
		t.Fatalf("containers asked = %v", cs.asked)
	}
	if host.calls != 1 {
		t.Fatalf("host calls = %d", host.calls)
	}
}

func TestRunMetricsReporterSendsServerMetrics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	m := NewMetricsReporter(&fakeHost{}, &fakeContainers{}, NewStatusSnapshot(&fakeStatus{}))
	got := make(chan sinkCall, 16)
	done := make(chan struct{})
	go func() {
		RunMetricsReporter(ctx, m, collectSink(got), 20*time.Millisecond, log)
		close(done)
	}()
	for i := 0; i < 2; i++ { // immediate + one tick
		select {
		case call := <-got:
			if call.kind != bus.EventServerMetrics {
				t.Fatalf("kind = %q", call.kind)
			}
			var s metrics.Sample
			if err := json.Unmarshal(call.payload, &s); err != nil || s.Server.MemTotal != 2 || s.Apps == nil {
				t.Fatalf("payload = %s (%v)", call.payload, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no report")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reporter did not stop")
	}
}

type fakeScanner struct {
	mu    sync.Mutex
	calls int
	// failAfter makes every scan after the first one fail.
	failAfter bool
}

func (f *fakeScanner) ScanDisk(context.Context) (metrics.DiskScan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failAfter && f.calls > 1 {
		return metrics.DiskScan{}, errors.New("df timed out")
	}
	return metrics.DiskScan{Docker: metrics.DockerDF{Volumes: float64(f.calls)}}, nil
}

func (f *fakeScanner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestRunDiskScannerKeepsLastGoodScan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	m := NewMetricsReporter(&fakeHost{}, &fakeContainers{}, NewStatusSnapshot(&fakeStatus{}))
	sc := &fakeScanner{failAfter: true}
	go RunDiskScanner(ctx, sc, m, 10*time.Millisecond, log)
	// Wait until at least two failing scans have run after the good one.
	deadline := time.Now().Add(2 * time.Second)
	for sc.count() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("scanner ran %d times", sc.count())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if s := m.Sample(time.Unix(1, 0)); s.Server.DockerDF == nil || s.Server.DockerDF.Volumes != 1 {
		t.Fatalf("failed scans must keep the last good one: %+v", s.Server.DockerDF)
	}
}

func TestStartMetricsHonoursConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	snap := NewStatusSnapshot(&fakeStatus{})
	got := make(chan sinkCall, 4)

	t.Setenv("METRICS_ENABLED", "false")
	if StartMetrics(ctx, cfg, snap, &fakeScanner{}, collectSink(got), log) {
		t.Fatal("started while disabled")
	}

	t.Setenv("METRICS_ENABLED", "true")
	t.Setenv("METRICS_DISK_INTERVAL", "0")
	sc := &fakeScanner{}
	if !StartMetrics(ctx, cfg, snap, sc, collectSink(got), log) {
		t.Fatal("not started")
	}
	select {
	case call := <-got:
		if call.kind != bus.EventServerMetrics {
			t.Fatalf("kind = %q", call.kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no first sample")
	}
	time.Sleep(50 * time.Millisecond)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.calls != 0 {
		t.Fatal("disk scan ran with METRICS_DISK_INTERVAL=0")
	}
}
