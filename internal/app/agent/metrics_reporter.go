package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/metrics"
	"winterflow/internal/infra/sysmetrics"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// MetricsInterval is the fixed sampling period of the metrics reporter.
const MetricsInterval = 60 * time.Second

// StatusSnapshot wraps the status source the status reporter polls every
// 30 s and remembers its last good answer, so the metrics reporter learns
// each app's containers without running `docker ps` a second time.
type StatusSnapshot struct {
	src  StatusSource
	mu   sync.RWMutex
	apps []command.AppStatus
	ok   bool
}

// NewStatusSnapshot wraps src.
func NewStatusSnapshot(src StatusSource) *StatusSnapshot { return &StatusSnapshot{src: src} }

// StatusReport is the wrapped source's report; a successful answer's apps
// are cached.
func (s *StatusSnapshot) StatusReport(ctx context.Context) (command.GetAppsStatusResponse, error) {
	resp, err := s.src.StatusReport(ctx)
	if err != nil {
		return command.GetAppsStatusResponse{}, err
	}
	s.mu.Lock()
	s.apps, s.ok = resp.Apps, true
	s.mu.Unlock()
	return resp, nil
}

// Latest returns the last good answer; ok is false before the first one.
func (s *StatusSnapshot) Latest() ([]command.AppStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.apps, s.ok
}

// HostCollector takes host samples (sysmetrics.Host).
type HostCollector interface {
	Collect(now time.Time) metrics.ServerSample
}

// ContainerCollector reads per-container usage (sysmetrics.Containers).
// LayoutKnown reports whether the host has a cgroup layout container
// metrics can be read from at all.
type ContainerCollector interface {
	Collect(now time.Time, ids []string) map[string]sysmetrics.ContainerStat
	LayoutKnown() bool
}

// DiskScanner measures disk usage (the Docker Compose orchestrator).
type DiskScanner interface {
	ScanDisk(ctx context.Context) (metrics.DiskScan, error)
}

// MetricsReporter assembles one metrics.Sample per tick from the host, the
// containers of the last status snapshot and the last disk scan.
type MetricsReporter struct {
	host       HostCollector
	containers ContainerCollector
	status     *StatusSnapshot

	mu   sync.RWMutex
	scan *metrics.DiskScan
}

// NewMetricsReporter wires the collectors.
func NewMetricsReporter(host HostCollector, containers ContainerCollector, status *StatusSnapshot) *MetricsReporter {
	return &MetricsReporter{host: host, containers: containers, status: status}
}

// SetDiskScan replaces the disk usage attached to every following sample.
func (m *MetricsReporter) SetDiskScan(s metrics.DiskScan) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scan = &s
}

func (m *MetricsReporter) diskScan() *metrics.DiskScan {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scan
}

// running reports whether a container has a live cgroup to read.
func running(c command.ContainerStatus) bool {
	return c.StatusCode == command.ContainerStatusActive || c.StatusCode == command.ContainerStatusRestarting
}

// Sample takes one sample at now. Only file reads: no exec.
func (m *MetricsReporter) Sample(now time.Time) metrics.Sample {
	apps, _ := m.status.Latest()
	var ids []string
	for _, a := range apps {
		for _, c := range a.Containers {
			if running(c) {
				ids = append(ids, c.ContainerID)
			}
		}
	}
	host := m.host.Collect(now)
	stats := m.containers.Collect(now, ids)
	return buildSample(now, host, apps, stats, m.containers.LayoutKnown, m.diskScan())
}

// buildSample sums container stats per app and attaches the disk scan.
// A running container whose cgroup did not resolve is skipped for the tick:
// the status snapshot is up to 30 s old, so after a redeploy it still holds
// the replaced containers, and a crash-looping one has no cgroup between
// attempts. Only when the host has no known cgroup layout at all
// (layoutKnown, probed lazily) is an app with nothing resolved flagged
// containers_unavailable. mem_limit is summed only when every resolved
// container has one; rate fields stay nil when no container has a rate.
func buildSample(now time.Time, host metrics.ServerSample, apps []command.AppStatus, stats map[string]sysmetrics.ContainerStat, layoutKnown func() bool, scan *metrics.DiskScan) metrics.Sample {
	if scan != nil {
		df := scan.Docker
		host.DockerDF = &df
	}
	out := metrics.Sample{TS: now.Unix(), Server: host, Apps: make([]metrics.AppSample, 0, len(apps))}
	for _, a := range apps {
		s := metrics.AppSample{AppID: a.AppID}
		var cpu, mem, limit, rx, tx float64
		var haveCPU, haveRx, haveTx bool
		live, resolved, limited := 0, 0, 0
		for _, c := range a.Containers {
			if !running(c) {
				continue
			}
			live++
			st, ok := stats[c.ContainerID]
			if !ok {
				continue
			}
			resolved++
			mem += st.MemUsed
			if st.MemLimit != nil {
				limited++
				limit += *st.MemLimit
			}
			if st.CPU != nil {
				cpu, haveCPU = cpu+*st.CPU, true
			}
			if st.NetRx != nil {
				rx, haveRx = rx+*st.NetRx, true
			}
			if st.NetTx != nil {
				tx, haveTx = tx+*st.NetTx, true
			}
		}
		switch {
		case live > 0 && resolved == 0:
			s.ContainersUnavailable = layoutKnown != nil && !layoutKnown()
		case resolved > 0:
			s.MemUsed = &mem
			if limited == resolved {
				s.MemLimit = &limit
			}
			if haveCPU {
				s.CPU = &cpu
			}
			if haveRx {
				s.NetRxBps = &rx
			}
			if haveTx {
				s.NetTxBps = &tx
			}
		}
		if scan != nil {
			if d, ok := scan.Apps[a.AppID]; ok {
				disk, image := d.Disk, d.Image
				s.Disk, s.Image = &disk, &image
			}
		}
		out.Apps = append(out.Apps, s)
	}
	return out
}

// RunMetricsReporter sends a server.metrics event immediately (it primes
// the counters: no rates yet) and then every interval until ctx is done.
// A send failure (stream down, server not claimed yet) skips the tick.
func RunMetricsReporter(ctx context.Context, m *MetricsReporter, sink EventSink, interval time.Duration, log *logger.Logger) {
	report := func(now time.Time) {
		payload, err := json.Marshal(m.Sample(now))
		if err != nil {
			log.Error("metrics report: marshal failed", "error", err)
			return
		}
		if err := sink(bus.EventServerMetrics, payload); err != nil {
			log.Debug("metrics report: send failed", "error", err)
		}
	}
	report(time.Now())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			report(now)
		}
	}
}

// RunDiskScanner measures disk usage now and then every interval, handing
// each result to m. A failed scan keeps the previous result.
func RunDiskScanner(ctx context.Context, scanner DiskScanner, m *MetricsReporter, interval time.Duration, log *logger.Logger) {
	scan := func() {
		s, err := scanner.ScanDisk(ctx)
		if err != nil {
			log.Warn("disk scan failed", "error", err)
			return
		}
		m.SetDiskScan(s)
	}
	scan()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scan()
		}
	}
}

// StartMetrics starts the metrics reporter (and, unless
// METRICS_DISK_INTERVAL=0, the disk scanner) when METRICS_ENABLED. status
// must be the snapshot the status reporter polls. It reports whether
// anything started. Both topologies call it; they differ only in sink.
func StartMetrics(ctx context.Context, cfg *config.ServerConfig, status *StatusSnapshot, scanner DiskScanner, sink EventSink, log *logger.Logger) bool {
	if !cfg.IsMetricsEnabled() {
		log.Info("metrics disabled (METRICS_ENABLED=false)")
		return false
	}
	dataDir, err := filepath.Abs(cfg.GetAgentDataDir())
	if err != nil {
		dataDir = cfg.GetAgentDataDir()
	}
	m := NewMetricsReporter(sysmetrics.NewHost("/", "/", dataDir), sysmetrics.NewContainers("/", runtime.NumCPU()), status)
	go RunMetricsReporter(ctx, m, sink, MetricsInterval, log)
	if d := cfg.GetMetricsDiskInterval(); d > 0 {
		go RunDiskScanner(ctx, scanner, m, d, log)
	}
	return true
}
