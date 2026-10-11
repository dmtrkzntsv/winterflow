// Package metrics holds the monitoring model shared by the agent (which
// produces samples) and the API (which stores, rolls up and serves them):
// the server.metrics event payload, its flattening into stored values, the
// storage tiers and the rollup math. It is transport- and storage-agnostic.
package metrics

import (
	"math"
	"sort"
	"time"
)

// Sample is the payload of one server.metrics event: the host sample plus
// one sample per app the agent has on disk. TS is the agent's clock, unix
// seconds; the API falls back to its own clock when TS is far off.
type Sample struct {
	TS     int64        `json:"ts"`
	Server ServerSample `json:"server"`
	Apps   []AppSample  `json:"apps"`
}

// ServerSample is the host part of a sample. Rate fields (cpu and every
// *_bps) are nil on the first sample after start and for an interval whose
// counter went backwards; temperature fields are nil/empty without sensors;
// DockerDF is nil until the first disk scan finished.
type ServerSample struct {
	CPU          *float64           `json:"cpu,omitempty"`
	MemUsed      float64            `json:"mem_used"`
	MemTotal     float64            `json:"mem_total"`
	SwapUsed     float64            `json:"swap_used"`
	SwapTotal    float64            `json:"swap_total"`
	Load1        float64            `json:"load1"`
	Load5        float64            `json:"load5"`
	Load15       float64            `json:"load15"`
	Disks        []Disk             `json:"disks"`
	DiskReadBps  *float64           `json:"disk_read_bps,omitempty"`
	DiskWriteBps *float64           `json:"disk_write_bps,omitempty"`
	NetRxBps     *float64           `json:"net_rx_bps,omitempty"`
	NetTxBps     *float64           `json:"net_tx_bps,omitempty"`
	Temp         *float64           `json:"temp,omitempty"`
	Temps        map[string]float64 `json:"temps,omitempty"`
	DockerDF     *DockerDF          `json:"docker_df,omitempty"`
}

// Disk is one filesystem (the root and the agent data dir's, deduplicated by
// device), in bytes.
type Disk struct {
	Mount string  `json:"mount"`
	Used  float64 `json:"used"`
	Total float64 `json:"total"`
}

// DockerDF is the daemon's disk usage from the last hourly scan, in bytes.
type DockerDF struct {
	Images      float64 `json:"images"`
	Volumes     float64 `json:"volumes"`
	BuildCache  float64 `json:"build_cache"`
	Reclaimable float64 `json:"reclaimable"`
}

// AppSample is one app's part of a sample: sums over its containers. When
// ContainersUnavailable is set (running containers whose cgroups resolve
// under no known layout) the container fields are nil. Disk and Image come
// from the last hourly scan (nil before it).
type AppSample struct {
	AppID                 string   `json:"app_id"`
	CPU                   *float64 `json:"cpu,omitempty"`
	MemUsed               *float64 `json:"mem_used,omitempty"`
	MemLimit              *float64 `json:"mem_limit,omitempty"`
	NetRxBps              *float64 `json:"net_rx_bps,omitempty"`
	NetTxBps              *float64 `json:"net_tx_bps,omitempty"`
	Disk                  *float64 `json:"disk,omitempty"`
	Image                 *float64 `json:"image,omitempty"`
	ContainersUnavailable bool     `json:"containers_unavailable,omitempty"`
}

// Values is one stored row's data: field -> value. Tier-1 rows hold plain
// values; rolled-up rows hold field (weighted average) and field+"_max".
type Values map[string]float64

// MaxSuffix marks a peak field in rolled-up data and chart points.
const MaxSuffix = "_max"

// TempPrefix prefixes one sensor's flattened temperature ("temp:Composite").
const TempPrefix = "temp:"

// maxSensors bounds the flattened per-sensor temperatures.
const maxSensors = 16

// put stores v unless it is NaN or infinite (JSON cannot carry them).
func (v Values) put(key string, x float64) {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return
	}
	v[key] = x
}

func (v Values) putPtr(key string, x *float64) {
	if x != nil {
		v.put(key, *x)
	}
}

// Values flattens the host sample into stored fields. disk_used/disk_total
// sum the filesystems, disk_pct is the fullest filesystem's percentage.
func (s ServerSample) Values() Values {
	v := Values{}
	v.putPtr("cpu", s.CPU)
	v.put("mem_used", s.MemUsed)
	v.put("mem_total", s.MemTotal)
	v.put("swap_used", s.SwapUsed)
	v.put("swap_total", s.SwapTotal)
	v.put("load1", s.Load1)
	v.put("load5", s.Load5)
	v.put("load15", s.Load15)
	if len(s.Disks) > 0 {
		var used, total, pct float64
		for _, d := range s.Disks {
			used += d.Used
			total += d.Total
			if d.Total > 0 {
				pct = math.Max(pct, 100*d.Used/d.Total)
			}
		}
		v.put("disk_used", used)
		v.put("disk_total", total)
		v.put("disk_pct", pct)
	}
	v.putPtr("disk_read_bps", s.DiskReadBps)
	v.putPtr("disk_write_bps", s.DiskWriteBps)
	v.putPtr("net_rx_bps", s.NetRxBps)
	v.putPtr("net_tx_bps", s.NetTxBps)
	v.putPtr("temp", s.Temp)
	labels := make([]string, 0, len(s.Temps))
	for l := range s.Temps {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	for i, l := range labels {
		if i == maxSensors {
			break
		}
		v.put(TempPrefix+l, s.Temps[l])
	}
	if s.DockerDF != nil {
		v.put("docker_images", s.DockerDF.Images)
		v.put("docker_volumes", s.DockerDF.Volumes)
		v.put("docker_build_cache", s.DockerDF.BuildCache)
		v.put("docker_reclaimable", s.DockerDF.Reclaimable)
	}
	return v
}

// Values flattens one app sample. unavailable is 1 when container metrics
// could not be read, so the UI can say so.
func (a AppSample) Values() Values {
	v := Values{}
	v.putPtr("cpu", a.CPU)
	v.putPtr("mem_used", a.MemUsed)
	v.putPtr("mem_limit", a.MemLimit)
	v.putPtr("net_rx_bps", a.NetRxBps)
	v.putPtr("net_tx_bps", a.NetTxBps)
	v.putPtr("disk", a.Disk)
	v.putPtr("image", a.Image)
	if a.ContainersUnavailable {
		v["unavailable"] = 1
	}
	return v
}

// clockSkewLimit bounds how far an agent's sample time may be from the API's
// clock before the API uses its own.
const clockSkewLimit = 5 * time.Minute

// SampleTime is when s was taken: the agent's TS unless it is missing or off
// by more than clockSkewLimit from now, then now. Always UTC.
func SampleTime(s Sample, now time.Time) time.Time {
	now = now.UTC()
	if s.TS <= 0 {
		return now
	}
	at := time.Unix(s.TS, 0).UTC()
	if d := at.Sub(now); d > clockSkewLimit || d < -clockSkewLimit {
		return now
	}
	return at
}
