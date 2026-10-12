// Package alert is the transport- and storage-agnostic alerting model: the
// metric catalog, org-wide rules with per-target overrides, the values a
// metrics sample yields per alert metric, the window buffer the evaluator
// averages over, and the notification text. Spec: monitoring-and-alerts §3.
package alert

import "slices"

// TargetKind is what a rule watches: a server or an app.
type TargetKind string

const (
	TargetServer TargetKind = "server"
	TargetApp    TargetKind = "app"
)

// Metric is an alertable measurement of a target.
type Metric string

const (
	MetricDown        Metric = "down"
	MetricCPU         Metric = "cpu"
	MetricMemory      Metric = "memory"
	MetricDisk        Metric = "disk"
	MetricLoad1       Metric = "load1"
	MetricLoad5       Metric = "load5"
	MetricLoad15      Metric = "load15"
	MetricBandwidth   Metric = "bandwidth"
	MetricTemperature Metric = "temperature"
)

// Unit says how a metric's values and thresholds read.
type Unit string

const (
	UnitNone    Unit = ""
	UnitPercent Unit = "%"
	UnitLoad    Unit = "load"
	UnitMBps    Unit = "MB/s"
	UnitCelsius Unit = "°C"
	UnitGB      Unit = "GB"
)

// Rule durations are whole minutes in [MinDuration, MaxDuration].
const (
	MinDuration = 1
	MaxDuration = 60
)

// MetricInfo describes one alertable metric of a target kind. Max bounds a
// threshold from above (0 = unbounded); thresholds are always > 0.
type MetricInfo struct {
	TargetKind TargetKind `json:"target_kind"`
	Metric     Metric     `json:"metric"`
	Label      string     `json:"label"`
	Unit       Unit       `json:"unit"`
	Max        float64    `json:"max"`
}

// HasThreshold reports whether the metric compares a value with a
// threshold; "down" has none.
func (m MetricInfo) HasThreshold() bool { return m.Metric != MetricDown }

// catalog is the spec's §3 metric table. Server disk is the fullest
// filesystem in percent; app disk is GB (volumes + app dir + layers).
var catalog = []MetricInfo{
	{TargetServer, MetricDown, "Down", UnitNone, 0},
	{TargetServer, MetricCPU, "CPU", UnitPercent, 100},
	{TargetServer, MetricMemory, "Memory", UnitPercent, 100},
	{TargetServer, MetricDisk, "Disk", UnitPercent, 100},
	{TargetServer, MetricLoad1, "Load 1m", UnitLoad, 0},
	{TargetServer, MetricLoad5, "Load 5m", UnitLoad, 0},
	{TargetServer, MetricLoad15, "Load 15m", UnitLoad, 0},
	{TargetServer, MetricBandwidth, "Bandwidth", UnitMBps, 0},
	{TargetServer, MetricTemperature, "Temperature", UnitCelsius, 150},
	{TargetApp, MetricDown, "Down", UnitNone, 0},
	{TargetApp, MetricCPU, "CPU", UnitPercent, 100},
	{TargetApp, MetricMemory, "Memory", UnitPercent, 100},
	{TargetApp, MetricDisk, "Disk", UnitGB, 0},
}

// Catalog returns every alertable metric, servers first.
func Catalog() []MetricInfo { return slices.Clone(catalog) }

// Lookup returns the catalog entry of a (kind, metric) pair.
func Lookup(kind TargetKind, m Metric) (MetricInfo, bool) {
	for _, info := range catalog {
		if info.TargetKind == kind && info.Metric == m {
			return info, true
		}
	}
	return MetricInfo{}, false
}
