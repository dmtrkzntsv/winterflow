# Monitoring PR 1 (metrics) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Collect server and app resource metrics every 60 s at near-zero idle cost, store them in tiered rollups with bounded retention, and show them as a dashboard control panel, a server Monitoring page, and app header blocks plus a Metrics tab.

**Architecture:** The agent (and standalone) reads procfs/sysfs/cgroupfs once a minute and sends one `server.metrics` event through the existing `EventSink`. It reuses the status reporter's `docker ps` snapshot and attaches an hourly `docker system df -v` disk scan. The API's events sink writes tier-1 rows into `metric_samples` (insert-or-ignore). A 10-minute loop in `WireCore` rolls rows up 1→10→20→120→480 minutes in Go and prunes them per tier. Three `GET /api/v1/metrics/*` routes serve the charts, which the SPA renders with the vendored shadcn `chart.tsx` (recharts 2.15).

**Tech Stack:** Go 1.25, sqlc over `database/sql` (SQLite `modernc.org/sqlite`, Postgres `pgx/v5`), chi + the typed route table / OpenAPI generator, React 19 + Vite + Vitest + Testing Library, recharts 2.15 via `web/src/components/ui/chart.tsx`.

**Spec:** `docs/superpowers/specs/2026-10-11-monitoring-and-alerts-design.md` (PR 1 parts: sections 1, 2, 4 without alert strip/badges/Alerts cards, 5, and the relevant parts of 6 and 7). Alerts and channels belong to PR 2 and are out of scope here.

The code in this plan was prototyped and run in a scratch copy of `main` + this branch. It passed `go vet ./...`, `go test ./...`, the metric/app repositories and the contract suite on Postgres 16 with `WF_CONTRACT_GUARD=require`, and `vitest run` / `tsc -b` / `eslint .`. Paste it as written. If a step's output differs from the expected output, stop and investigate before changing the code.

## Design decisions locked in (resolved spec ambiguities)

- **Payload home:** the event payload (`metrics.Sample`), its flattening (`Values`), the tiers, the rollup math and the in-memory latest store live in a new transport- and storage-agnostic package **`internal/domain/metrics`**, not in `internal/domain/command`. `command` is the catalog of *agent-bound commands* (request/response pairs (de)serialized by `codec`). `server.metrics` is an unsolicited *event* like `apps.status`, and both the agent (producer) and the API (storage, rollup, chart API) need the same types and math. Domain is the only layer both may import.
- **Stored fields** (tier-1 `data` JSON; rolled-up rows add `<field>_max`). Server rows: `cpu, mem_used, mem_total, swap_used, swap_total, load1, load5, load15, disk_used, disk_total` (sums over the filesystems), `disk_pct` (the fullest filesystem), `disk_read_bps, disk_write_bps, net_rx_bps, net_tx_bps, temp, temp:<label>` (at most 16 sensors), and `docker_images, docker_volumes, docker_build_cache, docker_reclaimable`. App rows: `cpu, mem_used, mem_limit, net_rx_bps, net_tx_bps, disk, image, unavailable` (1 = `containers_unavailable`). Per-mount filesystem detail stays in the in-memory latest sample (`disks[]`) and is not stored per minute.
- **Missing values are omitted, never zero:** rates on the first sample, after a counter goes backwards, `temp` without sensors, `docker_df`/`disk` before the first scan. Rollup averages each field over the rows that carry it, weighted by `samples`.
- **Sample time:** the agent sends `ts` (unix seconds). The API uses it unless it is missing or more than 5 min from its own clock (`metrics.SampleTime`).
- **Unknown apps:** `InsertSample` writes app rows only for app ids that the API's `apps` table holds *for that server* (this stops cross-server writes and rows for not-yet-reconciled apps). Rollup applies the same check, so a deleted app is not resurrected. App deletion (`AppRepository.DeleteApp` and the reconcile in `SyncApps`) deletes the app's samples **in the same transaction** as the app row. Server deletion cascades through the FK.
- **Migration types:** `0010_metrics.sql` exists in both engines with the same columns. Only `server_id` differs (`VARCHAR(36)` on SQLite, `UUID` on Postgres) so that it matches its FK target, as `0008_server_domains.sql` does. `app_id` is `VARCHAR(36) NOT NULL DEFAULT ''` on both (a UUID column cannot hold `''`). An extra index `(tier, bucket_start)` serves rollup and retention.
- **Latest sample "pruned on server delete":** there is no server-delete path in the API today. `LatestStore` has `Forget` (for PR 2 or a future delete route) and is pruned by age (1 h without samples) in the maintenance loop, so the map stays bounded.
- **Containers sharing the host netns:** the agent cannot `readlink /proc/<pid>/ns/net` of another user's process (verified on the dev host). A container whose `net/dev` lists `docker0`, `veth*` or `br-*` is treated as host-netns and reports no network.
- **Images and reclaimable from `docker system df -v`:** the verbose JSON has no exact shared-layer dedup. The values are: `images` = Σ `UniqueSize` + each distinct `SharedSize` once (within ~5 % of `docker system df` on the dev host); `reclaimable` = unique bytes of images with `Containers == "0"` + volumes with `Links == "0"` + build cache that is neither shared nor in use. Volumes and build cache match `docker system df` exactly.
- **`METRICS_DISK_INTERVAL`:** a Go duration. `0` disables the scan, unparsable values fall back to `1h`, and values under `5m` are raised to `5m`. `METRICS_ENABLED=false` turns collection off on an agent and storage plus maintenance off on an API. Chart routes then return empty series.
- **App header blocks** read the newest point of the app's `1h` series. No separate "latest per app" API exists.
- **Rollup lookback** is the source tier's retention (older source rows are gone anyway). Only groups that lack a destination row are JSON-decoded.

## Global Constraints

- No process forks in the 60 s path: host and container metrics are file reads only. The single exec is the hourly `docker system df -v`, with a 5-minute deadline.
- Every network and DB operation needs a deadline: the disk scan has `diskScanTimeout` and each maintenance tick has `metricsMaintenanceTimeout`. Metrics travel as events and never pass through the standalone `InProcessBridge`.
- No per-tick SSE fan-out of metrics. The UI pulls charts on demand and refreshes every 60 s only while the tab is visible.
- `.env.example` documents `METRICS_ENABLED` and `METRICS_DISK_INTERVAL` (default, effect, and which topology reads each) in the same change that adds the getters.
- SQL is written per engine. The migration file `0010_metrics.sql` exists in both `migrations/sqlite` and `migrations/pgsql`, every query has the same `-- name:` in `query/sqlite` and `query/pgsql`, and `make sqlc` runs after either changes. Never edit `sqlc/gen`. Add new type pairs to `sqlc/shapes_test.go`. Keep `.sql` files ASCII.
- Inside `Tx.WithTx(ctx, fn)` every query uses `r.tx.Querier(ctx)` with `fn`'s ctx (SQLite has one connection).
- After any route or response-type change, run `make openapi` and commit `api/openapi.yaml`. Also refresh `internal/app/web/testdata/routes.golden` with `go test ./internal/app/web -run TestRoutesGolden -update`.
- Server-addressed routes call `webutil.RequireServerAccess` before anything else. Contract `probes` entries make `TestForeignServer` assert 403 per route. Every new route needs a happy-path contract call below 400.
- Keep vendored shadcn: never delete or rewrite anything under `web/src/components/ui/`. Use `chart.tsx`, `card.tsx`, `progress.tsx` and `toggle-group.tsx` as they are.
- Components files export only components (`react-refresh/only-export-components`). Shared constants and helpers go in `web/src/lib/`.
- On this machine, every `go` build or test needs `GOTMPDIR=$HOME/.cache/wf-gotmp` (the tmpfs quota is too small). Create the directory once with `mkdir -p $HOME/.cache/wf-gotmp`.
- Every commit message ends with exactly the trailer line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Conventional Commits, imperative mood (`feat(metrics): ...`, `test(...)`).

## Review Focus

1. **Hosts without hwmon or with sparse procfs**: no sensors means no `temp`/`temps` (never 0 °C) and the Temperature chart is hidden. Tests: `TestHostWithoutSensorsOrFiles` (Task 3) and `hides the temperature chart without sensors` (Task 14).
2. **cgroup layout variants**: v2 systemd driver (`system.slice/docker-<id>.scope`), v2 cgroupfs driver (`docker/<id>`), v1 (`cpu,cpuacct` + `memory`), no layout at all (→ `containers_unavailable`), and host-netns containers. Tests: `TestContainersV2SystemdDriver`, `TestContainersV2CgroupfsDriverWithLimit`, `TestContainersV1Fallback`, `TestContainersUnknownLayoutAndPruning`, `TestContainersHostNetworkHasNoNet` (Task 4), and `TestBuildSampleSumsContainersPerApp` with its `blind` app (Task 6).
3. **Counters going backwards and the first sample**: a container restart (new cgroup, so `usage_usec` resets, and a new pid/netns) or a host counter reset must drop that interval, and the first sample carries no rates. Tests: `TestContainersRestartResetsCounters` (Task 4), `TestHostFirstSampleHasNoRates` and `TestHostCounterResetDropsTheInterval` (Task 3), `TestServerValuesFlattensAndOmitsMissingRates` (Task 1), and `says No data yet without rows or with only nulls` (Task 12).
4. **`docker system df -v` output**: human sizes `"1.2GB"`, `"0B"`, `"4.1kB"`, `"N/A"`, `"26.38GB (74%)"`, volumes shared by two apps (owned by neither), external volumes mounted by exactly one app, foreign compose projects, a hung daemon (timeout) and non-JSON output. Tests: `TestParseHumanSize`, `TestBuildDiskScan`, `TestScanDiskTimesOut`, `TestScanDiskBadJSON` (Task 5).
5. **Time-series edges**: gaps while a server is offline (no rows are invented, and the UI breaks the line), an app deleted between its samples and the rollup, replayed or duplicate samples (idempotent insert-or-ignore), samples for apps the API does not know, and agent clock skew. Tests: `TestRollupLeavesGapsAsGaps`, `TestRollupSkipsDeletedApps`, `TestRollupBuildsClosedBucketsOnce` (Task 8), `TestInsertSampleWritesServerAndKnownApps`, `TestDeleteAppRemovesItsMetricSamples` (Task 7), `breaks the line over a gap` (Task 11), and `TestSampleTimeFallsBackOnSkew` (Task 1).

### Task 1: Domain metrics model, tiers and rollup math

**Files:**
- Create: `internal/domain/metrics/sample.go`, `internal/domain/metrics/tier.go`, `internal/domain/metrics/rollup.go`, `internal/domain/metrics/latest.go`, `internal/domain/metrics/diskscan.go`
- Modify: `internal/infra/transport/bus/bus.go` (add `EventServerMetrics`)
- Test: `internal/domain/metrics/metrics_test.go`

**Interfaces:**
- Consumes: nothing (stdlib only).
- Produces (package `winterflow/internal/domain/metrics`):
  - `type Sample struct { TS int64 "ts"; Server ServerSample "server"; Apps []AppSample "apps" }`, which is the `server.metrics` payload.
  - `type ServerSample struct { CPU *float64; MemUsed, MemTotal, SwapUsed, SwapTotal, Load1, Load5, Load15 float64; Disks []Disk; DiskReadBps, DiskWriteBps, NetRxBps, NetTxBps, Temp *float64; Temps map[string]float64; DockerDF *DockerDF }` with JSON names from the spec (`cpu, mem_used, ... docker_df`).
  - `type Disk struct { Mount string; Used, Total float64 }`, `type DockerDF struct { Images, Volumes, BuildCache, Reclaimable float64 }`.
  - `type AppSample struct { AppID string; CPU, MemUsed, MemLimit, NetRxBps, NetTxBps, Disk, Image *float64; ContainersUnavailable bool }`.
  - `type Values map[string]float64`; `func (ServerSample) Values() Values`; `func (AppSample) Values() Values`; `const MaxSuffix = "_max"`; `const TempPrefix = "temp:"`.
  - `func SampleTime(s Sample, now time.Time) time.Time`.
  - `type Tier int` with `Tier1, Tier10, Tier20, Tier120, Tier480`; `var Tiers []Tier`; `type RollupPair struct{ From, To Tier }`; `var RollupPairs []RollupPair`; `const RollupGrace = 2 * time.Minute`; `func (Tier) Duration() time.Duration`; `func (Tier) Retention() time.Duration`; `func (Tier) BucketStart(time.Time) time.Time`.
  - `type Range string`; `func ParseRange(s string) (tier Tier, span time.Duration, ok bool)`.
  - `type Bucket struct { Start time.Time; Samples int; Data Values }`; `func Rollup(start time.Time, rows []Bucket) Bucket`; `type Point map[string]float64`; `func ToPoint(Bucket) Point`; `func (Point) Pick(fields ...string) Point`.
  - `type Latest struct { At time.Time "at"; Server ServerSample "server" }`; `type LatestStore`; `NewLatestStore()`, `(*LatestStore).Set/Get/Forget/Prune(cutoff) int`.
  - `type DiskScan struct { Docker DockerDF; Apps map[string]AppDisk }`; `type AppDisk struct { Disk, Image float64 }`.
  - `bus.EventServerMetrics EventKind = "server.metrics"`.

- [ ] **Write the failing test**

`internal/domain/metrics/metrics_test.go`:

```go
package metrics

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func f(x float64) *float64 { return &x }

func TestServerValuesFlattensAndOmitsMissingRates(t *testing.T) {
	s := ServerSample{
		MemUsed: 4e9, MemTotal: 8e9, Load1: 0.5,
		Disks: []Disk{{Mount: "/", Used: 30, Total: 100}, {Mount: "/data", Used: 90, Total: 100}},
		Temps: map[string]float64{"Composite": 41, "Package id 0": 55},
		Temp:  f(55),
	}
	v := s.Values()
	if _, ok := v["cpu"]; ok {
		t.Fatal("first sample has no cpu rate; the field must be absent, not zero")
	}
	if _, ok := v["net_rx_bps"]; ok {
		t.Fatal("missing rate stored")
	}
	if v["disk_used"] != 120 || v["disk_total"] != 200 || v["disk_pct"] != 90 {
		t.Fatalf("disk fields = %v", v)
	}
	if v["temp"] != 55 || v["temp:Composite"] != 41 || v["temp:Package id 0"] != 55 {
		t.Fatalf("temps = %v", v)
	}
	if _, ok := v["docker_images"]; ok {
		t.Fatal("docker_df stored before the first scan")
	}
}

func TestServerValuesDropsNaNAndCapsSensors(t *testing.T) {
	temps := map[string]float64{}
	for i := 0; i < 40; i++ {
		temps[string(rune('a'+i%26))+string(rune('a'+i/26))] = float64(i)
	}
	v := ServerSample{CPU: f(math.NaN()), Temps: temps}.Values()
	if _, ok := v["cpu"]; ok {
		t.Fatal("NaN stored")
	}
	n := 0
	for k := range v {
		if len(k) > len(TempPrefix) && k[:len(TempPrefix)] == TempPrefix {
			n++
		}
	}
	if n != maxSensors {
		t.Fatalf("stored %d sensors, want %d", n, maxSensors)
	}
	if _, err := json.Marshal(v); err != nil {
		t.Fatal(err)
	}
}

func TestAppValues(t *testing.T) {
	v := AppSample{AppID: "a", CPU: f(12.5), MemUsed: f(100), Disk: f(5e9), ContainersUnavailable: true}.Values()
	if v["cpu"] != 12.5 || v["mem_used"] != 100 || v["disk"] != 5e9 || v["unavailable"] != 1 {
		t.Fatalf("app values = %v", v)
	}
	if _, ok := v["mem_limit"]; ok {
		t.Fatal("no limit stored as a value")
	}
}

func TestSampleTimeFallsBackOnSkew(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 30, 0, time.UTC)
	if got := SampleTime(Sample{TS: now.Add(-40 * time.Second).Unix()}, now); !got.Equal(now.Add(-40 * time.Second)) {
		t.Fatalf("in-range ts = %v", got)
	}
	for _, ts := range []int64{0, -5, now.Add(time.Hour).Unix(), now.Add(-time.Hour).Unix()} {
		if got := SampleTime(Sample{TS: ts}, now); !got.Equal(now) {
			t.Fatalf("ts %d -> %v, want now", ts, got)
		}
	}
}

func TestParseRange(t *testing.T) {
	cases := map[string]Tier{"1h": Tier1, "12h": Tier10, "24h": Tier20, "7d": Tier120, "30d": Tier480}
	for r, want := range cases {
		tier, span, ok := ParseRange(r)
		if !ok || tier != want || span < time.Hour {
			t.Errorf("%s -> %v %v %v", r, tier, span, ok)
		}
		// Each range fits its tier's retention: the chart is never cut short.
		if span > tier.Retention() {
			t.Errorf("%s spans %v but tier %d keeps %v", r, span, tier, tier.Retention())
		}
	}
	for _, bad := range []string{"", "2h", "1H", "30d "} {
		if _, _, ok := ParseRange(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestBucketStartAlignsInUTC(t *testing.T) {
	at := time.Date(2026, 10, 11, 17, 47, 13, 0, time.FixedZone("PDT", -7*3600)) // 00:47:13 UTC next day
	cases := map[Tier]time.Time{
		Tier1:   time.Date(2026, 10, 12, 0, 47, 0, 0, time.UTC),
		Tier10:  time.Date(2026, 10, 12, 0, 40, 0, 0, time.UTC),
		Tier20:  time.Date(2026, 10, 12, 0, 40, 0, 0, time.UTC),
		Tier120: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
		Tier480: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
	}
	for tier, want := range cases {
		if got := tier.BucketStart(at); !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("tier %d: %v, want %v", tier, got, want)
		}
	}
}

func TestRollupWeightedAverageAndMaxOfMax(t *testing.T) {
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	// Two tier-10 rows: 10 samples averaging 20 (peak 50), 5 samples averaging
	// 80 (peak 90). Weighted avg = (200+400)/15 = 40, not the plain 50.
	b := Rollup(start, []Bucket{
		{Start: start, Samples: 10, Data: Values{"cpu": 20, "cpu_max": 50}},
		{Start: start.Add(10 * time.Minute), Samples: 5, Data: Values{"cpu": 80, "cpu_max": 90}},
	})
	if b.Samples != 15 || b.Data["cpu"] != 40 || b.Data["cpu_max"] != 90 {
		t.Fatalf("rollup = %+v", b)
	}
}

func TestRollupFromTier1UsesValuesAsPeaksAndSkipsMissingFields(t *testing.T) {
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	b := Rollup(start, []Bucket{
		{Start: start, Samples: 1, Data: Values{"mem_used": 10}}, // first sample: no cpu yet
		{Start: start.Add(time.Minute), Samples: 1, Data: Values{"mem_used": 30, "cpu": 7}},
		{Start: start.Add(2 * time.Minute), Samples: 1, Data: Values{"mem_used": 20, "cpu": 3}},
	})
	if b.Data["mem_used"] != 20 || b.Data["mem_used_max"] != 30 {
		t.Fatalf("mem = %v", b.Data)
	}
	// cpu averages over the two rows that have it, not three.
	if b.Data["cpu"] != 5 || b.Data["cpu_max"] != 7 {
		t.Fatalf("cpu = %v", b.Data)
	}
	if b.Samples != 3 {
		t.Fatalf("samples = %d", b.Samples)
	}
}

func TestRollupIsIdempotentOverItsOwnOutput(t *testing.T) {
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	once := Rollup(start, []Bucket{{Start: start, Samples: 4, Data: Values{"cpu": 10, "cpu_max": 12}}})
	twice := Rollup(start, []Bucket{once})
	if twice.Data["cpu"] != 10 || twice.Data["cpu_max"] != 12 || twice.Samples != 4 {
		t.Fatalf("re-rollup changed values: %+v", twice)
	}
}

func TestToPointFillsPeaksForTier1(t *testing.T) {
	start := time.Unix(1_760_000_000, 0).UTC()
	p := ToPoint(Bucket{Start: start, Samples: 1, Data: Values{"cpu": 3}})
	if p["t"] != 1_760_000_000 || p["cpu"] != 3 || p["cpu_max"] != 3 {
		t.Fatalf("point = %v", p)
	}
	p = ToPoint(Bucket{Start: start, Samples: 10, Data: Values{"cpu": 3, "cpu_max": 9}})
	if p["cpu_max"] != 9 {
		t.Fatalf("stored peak overwritten: %v", p)
	}
	picked := p.Pick("cpu", "absent")
	if len(picked) != 3 || picked["t"] != 1_760_000_000 {
		t.Fatalf("pick = %v", picked)
	}
}

func TestLatestStorePrune(t *testing.T) {
	s := NewLatestStore()
	now := time.Now()
	s.Set("old", Latest{At: now.Add(-2 * time.Hour)})
	s.Set("new", Latest{At: now})
	if n := s.Prune(now.Add(-time.Hour)); n != 1 {
		t.Fatalf("pruned %d", n)
	}
	if _, ok := s.Get("old"); ok {
		t.Fatal("stale entry kept")
	}
	s.Forget("new")
	if _, ok := s.Get("new"); ok {
		t.Fatal("forgotten entry kept")
	}
}
```

- [ ] **Run it, expect FAIL** (package does not exist yet)

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/domain/metrics/
```
Expected: `no non-test Go files` / `undefined: ServerSample` compile failure.

- [ ] **Implement**

`internal/domain/metrics/sample.go`:

```go
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
```

`internal/domain/metrics/tier.go`:

```go
package metrics

import "time"

// Tier is a storage resolution in minutes. Tier1 rows are raw samples; each
// higher tier is rolled up from the one before it.
type Tier int

const (
	Tier1   Tier = 1
	Tier10  Tier = 10
	Tier20  Tier = 20
	Tier120 Tier = 120
	Tier480 Tier = 480
)

// Tiers lists every tier, finest first.
var Tiers = []Tier{Tier1, Tier10, Tier20, Tier120, Tier480}

// RollupPair is one rollup step: closed To-buckets are built from From rows.
type RollupPair struct{ From, To Tier }

// RollupPairs are the rollup steps, in the order they must run.
var RollupPairs = []RollupPair{{Tier1, Tier10}, {Tier10, Tier20}, {Tier20, Tier120}, {Tier120, Tier480}}

// RollupGrace keeps a bucket open this long after its end, so a sample still
// in flight on the bus lands before the bucket is rolled up.
const RollupGrace = 2 * time.Minute

// Duration is the bucket length.
func (t Tier) Duration() time.Duration { return time.Duration(t) * time.Minute }

// Retention is how long rows of the tier are kept.
func (t Tier) Retention() time.Duration {
	switch t {
	case Tier1:
		return time.Hour
	case Tier10:
		return 12 * time.Hour
	case Tier20:
		return 24 * time.Hour
	case Tier120:
		return 7 * 24 * time.Hour
	default:
		return 30 * 24 * time.Hour
	}
}

// BucketStart aligns t (in UTC) down to the tier's bucket.
func (t Tier) BucketStart(at time.Time) time.Time {
	return at.UTC().Truncate(t.Duration())
}

// Range is a chart range the API accepts.
type Range string

const (
	Range1h  Range = "1h"
	Range12h Range = "12h"
	Range24h Range = "24h"
	Range7d  Range = "7d"
	Range30d Range = "30d"
)

// ParseRange maps a chart range to the tier that serves it and the span it
// covers; ok is false for an unknown range.
func ParseRange(s string) (tier Tier, span time.Duration, ok bool) {
	switch Range(s) {
	case Range1h:
		return Tier1, time.Hour, true
	case Range12h:
		return Tier10, 12 * time.Hour, true
	case Range24h:
		return Tier20, 24 * time.Hour, true
	case Range7d:
		return Tier120, 7 * 24 * time.Hour, true
	case Range30d:
		return Tier480, 30 * 24 * time.Hour, true
	}
	return 0, 0, false
}
```

`internal/domain/metrics/rollup.go`:

```go
package metrics

import (
	"math"
	"strings"
	"time"
)

// Bucket is one stored row: its start, how many raw samples it stands for,
// and its values.
type Bucket struct {
	Start   time.Time
	Samples int
	Data    Values
}

// Rollup merges the rows of one target that fall into the destination bucket
// starting at start. Every field gets its average weighted by the rows'
// sample counts (over the rows that carry the field) and field+"_max": the
// largest of the rows' peaks, where a row without a peak (tier 1) counts its
// value. Samples is the sum of the rows' counts.
func Rollup(start time.Time, rows []Bucket) Bucket {
	type acc struct{ sum, weight, max float64 }
	accs := map[string]*acc{}
	total := 0
	for _, r := range rows {
		n := r.Samples
		if n < 1 {
			n = 1
		}
		total += n
		for k, x := range r.Data {
			if strings.HasSuffix(k, MaxSuffix) {
				continue
			}
			peak := x
			if m, ok := r.Data[k+MaxSuffix]; ok {
				peak = m
			}
			a := accs[k]
			if a == nil {
				a = &acc{max: math.Inf(-1)}
				accs[k] = a
			}
			a.sum += x * float64(n)
			a.weight += float64(n)
			a.max = math.Max(a.max, peak)
		}
	}
	out := Values{}
	for k, a := range accs {
		out.put(k, a.sum/a.weight)
		out.put(k+MaxSuffix, a.max)
	}
	return Bucket{Start: start.UTC(), Samples: total, Data: out}
}

// Point is one chart point: "t" (the bucket start, unix seconds), every
// field's average, and every field's "<field>_max" peak.
type Point map[string]float64

// ToPoint renders a stored row as a chart point. Tier-1 rows carry no peaks;
// their peak is the value itself, so every point has both.
func ToPoint(b Bucket) Point {
	p := Point{"t": float64(b.Start.Unix())}
	for k, x := range b.Data {
		p[k] = x
	}
	for k, x := range b.Data {
		if strings.HasSuffix(k, MaxSuffix) {
			continue
		}
		if _, ok := b.Data[k+MaxSuffix]; !ok {
			p[k+MaxSuffix] = x
		}
	}
	return p
}

// Pick keeps "t" and the named fields with their peaks.
func (p Point) Pick(fields ...string) Point {
	out := Point{"t": p["t"]}
	for _, f := range fields {
		if x, ok := p[f]; ok {
			out[f] = x
		}
		if x, ok := p[f+MaxSuffix]; ok {
			out[f+MaxSuffix] = x
		}
	}
	return out
}
```

`internal/domain/metrics/latest.go`:

```go
package metrics

import (
	"sync"
	"time"
)

// Latest is one server's most recent host sample, for the dashboard meters.
type Latest struct {
	At     time.Time    `json:"at"`
	Server ServerSample `json:"server"`
}

// LatestStore keeps the latest host sample per server in memory. Entries are
// replaced on every sample and dropped by Forget or once older than Prune's
// cutoff, so the map is bounded by the servers reporting recently.
type LatestStore struct {
	mu sync.RWMutex
	m  map[string]Latest
}

// NewLatestStore returns an empty store.
func NewLatestStore() *LatestStore { return &LatestStore{m: map[string]Latest{}} }

// Set records serverID's newest sample.
func (s *LatestStore) Set(serverID string, l Latest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[serverID] = l
}

// Get returns serverID's newest sample.
func (s *LatestStore) Get(serverID string) (Latest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.m[serverID]
	return l, ok
}

// Forget drops serverID (its server was deleted).
func (s *LatestStore) Forget(serverID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, serverID)
}

// Prune drops entries taken before cutoff and reports how many it dropped.
func (s *LatestStore) Prune(cutoff time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, l := range s.m {
		if l.At.Before(cutoff) {
			delete(s.m, id)
			n++
		}
	}
	return n
}
```

`internal/domain/metrics/diskscan.go`:

```go
package metrics

// DiskScan is the result of one hourly disk measurement (docker system df
// plus a walk of each app directory), attached to every sample until the
// next scan replaces it.
type DiskScan struct {
	Docker DockerDF
	// Apps maps an app id to its disk usage.
	Apps map[string]AppDisk
}

// AppDisk is one app's disk usage in bytes. Disk is volumes + app directory
// + container writable layers; Image is the size of the images its
// containers run, reported apart because images can be shared.
type AppDisk struct {
	Disk  float64
	Image float64
}
```

Apply to `internal/infra/transport/bus/bus.go`:

```diff
--- a/internal/infra/transport/bus/bus.go
+++ b/internal/infra/transport/bus/bus.go
@@ -55,6 +55,9 @@
 	EventCapabilities EventKind = "server.capabilities"
 	// EventAppsStatus reports container status for a server's apps.
 	EventAppsStatus EventKind = "apps.status"
+	// EventServerMetrics carries one minute's resource sample of a server
+	// and its apps (payload: metrics.Sample).
+	EventServerMetrics EventKind = "server.metrics"
 )
 
 // EventMessage is the events-queue payload: an unsolicited event from a server's
```

- [ ] **Run it, expect PASS**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/domain/metrics/ && GOTMPDIR=$HOME/.cache/wf-gotmp go vet ./internal/infra/transport/bus/
```
Expected: `ok  winterflow/internal/domain/metrics`.

- [ ] **Commit**

```bash
git add internal/domain/metrics internal/infra/transport/bus/bus.go
git commit -m "feat(metrics): add the domain metrics model, tiers and rollup math" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 2: Config getters and .env.example

**Files:**
- Modify: `pkg/config/config.go`, `.env.example`
- Test: `pkg/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func (c *ServerConfig) IsMetricsEnabled() bool` (default true; `false`/`0`/`no`/`off` disable) and `func (c *ServerConfig) GetMetricsDiskInterval() time.Duration` (default `time.Hour`, `0` → 0 = disabled, invalid → 1h, minimum 5m).

- [ ] **Write the failing test** (the diff adds `"time"` to the imports and the two tests at the end of the file)

Apply to `pkg/config/config_test.go`:

```diff
--- a/pkg/config/config_test.go
+++ b/pkg/config/config_test.go
@@ -4,6 +4,7 @@
 	"os"
 	"path/filepath"
 	"testing"
+	"time"
 )
 
 // unsetenv guarantees key is absent for the duration of the test, restoring
@@ -409,3 +410,36 @@
 		t.Fatalf("GetIngressTrustedProxies() = %q", got)
 	}
 }
+
+func TestIsMetricsEnabled(t *testing.T) {
+	c := NewServerConfig("standalone")
+	cases := map[string]bool{"": true, "true": true, "1": true, "false": false, "FALSE": false, "0": false, "off": false, "junk": true}
+	for val, want := range cases {
+		if val == "" {
+			unsetenv(t, "METRICS_ENABLED")
+		} else {
+			t.Setenv("METRICS_ENABLED", val)
+		}
+		if got := c.IsMetricsEnabled(); got != want {
+			t.Errorf("METRICS_ENABLED=%q: got %v, want %v", val, got, want)
+		}
+	}
+}
+
+func TestGetMetricsDiskInterval(t *testing.T) {
+	c := NewServerConfig("standalone")
+	cases := map[string]time.Duration{
+		"": time.Hour, "0": 0, "0s": 0, "2h": 2 * time.Hour, "30m": 30 * time.Minute,
+		"1m": 5 * time.Minute, "junk": time.Hour, "-1h": time.Hour,
+	}
+	for val, want := range cases {
+		if val == "" {
+			unsetenv(t, "METRICS_DISK_INTERVAL")
+		} else {
+			t.Setenv("METRICS_DISK_INTERVAL", val)
+		}
+		if got := c.GetMetricsDiskInterval(); got != want {
+			t.Errorf("METRICS_DISK_INTERVAL=%q: got %v, want %v", val, got, want)
+		}
+	}
+}
```

- [ ] **Run it, expect FAIL**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./pkg/config/ -run Metrics
```
Expected: `c.IsMetricsEnabled undefined`.

- [ ] **Implement** (getters plus `"time"` import; the `.env.example` section sits right before the Ingress section)

Apply to `pkg/config/config.go`:

```diff
--- a/pkg/config/config.go
+++ b/pkg/config/config.go
@@ -6,6 +6,7 @@
 	"path/filepath"
 	"strconv"
 	"strings"
+	"time"
 )
 
 type ServerConfig struct {
@@ -185,6 +186,43 @@
 	}
 }
 
+// IsMetricsEnabled reports whether resource metrics are collected and sent
+// (agent, standalone) and stored (API). Default on; METRICS_ENABLED=false
+// (or 0) turns the whole pipeline off.
+func (c *ServerConfig) IsMetricsEnabled() bool {
+	switch strings.ToLower(strings.TrimSpace(os.Getenv("METRICS_ENABLED"))) {
+	case "false", "0", "no", "off":
+		return false
+	default:
+		return true
+	}
+}
+
+// minMetricsDiskInterval keeps the disk scan (one `docker system df -v`,
+// ~45 s of daemon work on a busy box) from being scheduled back to back.
+const minMetricsDiskInterval = 5 * time.Minute
+
+// GetMetricsDiskInterval is how often the agent measures per-app disk usage
+// (default 1h). "0" disables the scan; an unparsable value falls back to 1h
+// and anything shorter than 5m is raised to 5m.
+func (c *ServerConfig) GetMetricsDiskInterval() time.Duration {
+	v := strings.TrimSpace(os.Getenv("METRICS_DISK_INTERVAL"))
+	if v == "" {
+		return time.Hour
+	}
+	if v == "0" {
+		return 0
+	}
+	d, err := time.ParseDuration(v)
+	if err != nil || d < 0 {
+		return time.Hour
+	}
+	if d == 0 {
+		return 0
+	}
+	return max(d, minMetricsDiskInterval)
+}
+
 // GetIngressHTTPPort is the embedded proxy's HTTP port (default 80).
 func (c *ServerConfig) GetIngressHTTPPort() int {
 	if v, err := strconv.Atoi(os.Getenv("INGRESS_HTTP_PORT")); err == nil && v > 0 {
```

Apply to `.env.example`:

```diff
--- a/.env.example
+++ b/.env.example
@@ -124,6 +124,22 @@
 # (standard Docker variable; default ~/.docker).
 #DOCKER_CONFIG=
 
+# --- Monitoring -----------------------------------------------------------------
+
+# Resource metrics. Agent and standalone: sample host and container CPU,
+# memory, disk, network and temperature every 60 s from procfs/cgroupfs (file
+# reads, no process forks) and send them as server.metrics events. API and
+# standalone: store them, roll them up (10 min -> 8 h buckets) and serve the
+# charts. false (or 0) turns collection off on an agent and storage off on an
+# API. Default true.
+#METRICS_ENABLED=true
+
+# Agent/standalone: how often per-app disk usage is measured, with one
+# `docker system df -v` (bounded by a 5-minute timeout; ~45 s on a busy box)
+# plus a walk of each app directory. Go duration; default 1h, 0 disables,
+# values under 5m are raised to 5m.
+#METRICS_DISK_INTERVAL=1h
+
 # --- Ingress (embedded Caddy; agent/standalone only) ----------------------------
 
 # Master switch for the embedded reverse proxy. Default OFF (opt-in) — many
```

- [ ] **Run it, expect PASS**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./pkg/config/
```
Expected: `ok  winterflow/pkg/config`.

- [ ] **Commit**

```bash
git add pkg/config/config.go pkg/config/config_test.go .env.example
git commit -m "feat(config): add METRICS_ENABLED and METRICS_DISK_INTERVAL" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 3: Host collector (procfs/sysfs, injectable root)

**Files:**
- Create: `internal/infra/sysmetrics/procfs.go`, `internal/infra/sysmetrics/hwmon.go`, `internal/infra/sysmetrics/statfs_linux.go`, `internal/infra/sysmetrics/statfs_other.go`, `internal/infra/sysmetrics/host.go`
- Test: `internal/infra/sysmetrics/fixture_test.go`, `internal/infra/sysmetrics/host_test.go`

**Interfaces:**
- Consumes: `metrics.ServerSample`, `metrics.Disk` (Task 1).
- Produces (package `winterflow/internal/infra/sysmetrics`): `func NewHost(root string, mounts ...string) *Host`; `func (h *Host) Collect(now time.Time) metrics.ServerSample`. Internals that Task 4 reuses are `parseNetDev(b []byte) []netDev`, `hostNetns([]netDev) bool`, `sumNet([]netDev, func(string) bool) (rx, tx uint64)`, `rate(prev, cur uint64, secs float64) *float64`, and `readTrim(path) string`. `Host.statFS func(path string) (fsInfo, error)` is a field that tests replace.

- [ ] **Write the failing tests** (the fixture helper is shared with Task 4)

`internal/infra/sysmetrics/fixture_test.go`:

```go
package sysmetrics

import (
	"os"
	"path/filepath"
	"testing"
)

// tree writes files (relative path -> content) under a fresh root and
// returns it. A path ending in "/" creates a directory.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, files)
	return root
}

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if rel[len(rel)-1] == '/' {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
```

`internal/infra/sysmetrics/host_test.go`:

```go
package sysmetrics

import (
	"errors"
	"testing"
	"time"
)

const procStat1 = "cpu  100 0 100 700 100 0 0 0 0 0\ncpu0 50 0 50 350 50 0 0 0 0 0\nintr 1 2 3\n"

// +100 busy (user 60, system 40) and +100 idle (idle 80, iowait 20): 50 %.
const procStat2 = "cpu  160 0 140 780 120 0 0 0 0 0\ncpu0 80 0 70 390 60 0 0 0 0 0\n"

const meminfo = `MemTotal:        8000000 kB
MemFree:          500000 kB
MemAvailable:    6000000 kB
Buffers:          100000 kB
Cached:          2000000 kB
SwapTotal:       1000000 kB
SwapFree:         750000 kB
`

const diskstats1 = `   7       0 loop0 11 0 9999 1 0 0 9999 0 0 2 1 0 0 0 0 0 0
 259       0 nvme0n1 100 0 1000 10 50 0 2000 5 0 0 0 0 0 0 0 0 0
 259       1 nvme0n1p1 90 0 900 10 40 0 1800 5 0 0 0 0 0 0 0 0 0
 252       0 zram0 5 0 4000 0 5 0 4000 0 0 0 0 0 0 0 0 0 0
 253       0 dm-0 5 0 4000 0 5 0 4000 0 0 0 0 0 0 0 0 0 0
`

// nvme0n1 +120 sectors read, +240 written over 60 s: 1024 B/s and 2048 B/s.
const diskstats2 = `   7       0 loop0 11 0 99999 1 0 0 99999 0 0 2 1 0 0 0 0 0 0
 259       0 nvme0n1 100 0 1120 10 50 0 2240 5 0 0 0 0 0 0 0 0 0
 259       1 nvme0n1p1 90 0 1020 10 40 0 2040 5 0 0 0 0 0 0 0 0 0
 252       0 zram0 5 0 8000 0 5 0 8000 0 0 0 0 0 0 0 0 0 0
 253       0 dm-0 5 0 8000 0 5 0 8000 0 0 0 0 0 0 0 0 0 0
`

const netdevHeader = "Inter-|   Receive                                                |  Transmit\n face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n"

const netdev1 = netdevHeader +
	"    lo: 5000 1 0 0 0 0 0 0 5000 1 0 0 0 0 0 0\n" +
	"  eth0: 1000 1 0 0 0 0 0 0 2000 1 0 0 0 0 0 0\n" +
	"docker0: 7000 1 0 0 0 0 0 0 7000 1 0 0 0 0 0 0\n" +
	"veth1a2b: 7000 1 0 0 0 0 0 0 7000 1 0 0 0 0 0 0\n" +
	"br-0f3e: 7000 1 0 0 0 0 0 0 7000 1 0 0 0 0 0 0\n"

// eth0 +6000 rx, +12000 tx over 60 s; the excluded interfaces move a lot.
const netdev2 = netdevHeader +
	"    lo: 905000 1 0 0 0 0 0 0 905000 1 0 0 0 0 0 0\n" +
	"  eth0: 7000 1 0 0 0 0 0 0 14000 1 0 0 0 0 0 0\n" +
	"docker0: 907000 1 0 0 0 0 0 0 907000 1 0 0 0 0 0 0\n" +
	"veth1a2b: 907000 1 0 0 0 0 0 0 907000 1 0 0 0 0 0 0\n" +
	"br-0f3e: 907000 1 0 0 0 0 0 0 907000 1 0 0 0 0 0 0\n"

func hostFixture(t *testing.T) string {
	return tree(t, map[string]string{
		"proc/stat":                          procStat1,
		"proc/meminfo":                       meminfo,
		"proc/loadavg":                       "0.52 0.41 0.30 2/345 6789\n",
		"proc/diskstats":                     diskstats1,
		"proc/net/dev":                       netdev1,
		"sys/block/nvme0n1/":                 "",
		"sys/block/loop0/":                   "",
		"sys/block/zram0/":                   "",
		"sys/block/dm-0/":                    "",
		"sys/class/hwmon/hwmon0/name":        "acpitz\n",
		"sys/class/hwmon/hwmon0/temp1_input": "27800\n",
		"sys/class/hwmon/hwmon1/name":        "coretemp\n",
		"sys/class/hwmon/hwmon1/temp1_input": "55000\n",
		"sys/class/hwmon/hwmon1/temp1_label": "Package id 0\n",
		"sys/class/hwmon/hwmon1/temp2_input": "0\n",
		"sys/class/hwmon/hwmon2/name":        "acpitz\n",
		"sys/class/hwmon/hwmon2/temp1_input": "30000\n",
	})
}

func fakeStatFS(m map[string]fsInfo) func(string) (fsInfo, error) {
	return func(p string) (fsInfo, error) {
		fi, ok := m[p]
		if !ok {
			return fsInfo{}, errors.New("no such mount")
		}
		return fi, nil
	}
}

func TestHostFirstSampleHasNoRates(t *testing.T) {
	h := NewHost(hostFixture(t), "/", "/srv/data")
	h.statFS = fakeStatFS(map[string]fsInfo{
		"/":         {dev: 1, used: 40, total: 100},
		"/srv/data": {dev: 1, used: 40, total: 100}, // same filesystem: deduped
	})
	s := h.Collect(time.Unix(1000, 0))
	if s.CPU != nil || s.DiskReadBps != nil || s.NetRxBps != nil {
		t.Fatalf("first sample carries rates: %+v", s)
	}
	if s.MemTotal != 8000000*1024 || s.MemUsed != 2000000*1024 {
		t.Fatalf("mem = %v / %v", s.MemUsed, s.MemTotal)
	}
	if s.SwapTotal != 1000000*1024 || s.SwapUsed != 250000*1024 {
		t.Fatalf("swap = %v / %v", s.SwapUsed, s.SwapTotal)
	}
	if s.Load1 != 0.52 || s.Load5 != 0.41 || s.Load15 != 0.30 {
		t.Fatalf("load = %v %v %v", s.Load1, s.Load5, s.Load15)
	}
	if len(s.Disks) != 1 || s.Disks[0].Mount != "/" || s.Disks[0].Used != 40 {
		t.Fatalf("disks = %+v", s.Disks)
	}
	if s.Temp == nil || *s.Temp != 55 {
		t.Fatalf("temp = %v", s.Temp)
	}
	want := map[string]float64{"acpitz": 27.8, "Package id 0": 55, "acpitz #2": 30}
	if len(s.Temps) != len(want) {
		t.Fatalf("temps = %v", s.Temps)
	}
	for k, v := range want {
		if s.Temps[k] != v {
			t.Fatalf("temps = %v, want %v", s.Temps, want)
		}
	}
}

func TestHostRatesAgainstPreviousSample(t *testing.T) {
	root := hostFixture(t)
	h := NewHost(root, "/")
	h.statFS = fakeStatFS(nil)
	h.Collect(time.Unix(1000, 0))
	write(t, root, map[string]string{"proc/stat": procStat2, "proc/diskstats": diskstats2, "proc/net/dev": netdev2})
	s := h.Collect(time.Unix(1060, 0))
	if s.CPU == nil || *s.CPU != 50 {
		t.Fatalf("cpu = %v", s.CPU)
	}
	if s.DiskReadBps == nil || *s.DiskReadBps != 1024 || *s.DiskWriteBps != 2048 {
		t.Fatalf("disk io = %v %v (partitions, loop, zram, dm must not count)", s.DiskReadBps, s.DiskWriteBps)
	}
	if s.NetRxBps == nil || *s.NetRxBps != 100 || *s.NetTxBps != 200 {
		t.Fatalf("net = %v %v (lo, docker0, veth, br- must not count)", s.NetRxBps, s.NetTxBps)
	}
	if len(s.Disks) != 0 {
		t.Fatalf("unmeasurable mount reported: %+v", s.Disks)
	}
}

func TestHostCounterResetDropsTheInterval(t *testing.T) {
	root := hostFixture(t)
	h := NewHost(root, "/")
	h.statFS = fakeStatFS(nil)
	h.Collect(time.Unix(1000, 0))
	write(t, root, map[string]string{"proc/stat": procStat2, "proc/diskstats": diskstats2, "proc/net/dev": netdev2})
	h.Collect(time.Unix(1060, 0))
	// Counters back at their old values (e.g. an interface re-created).
	write(t, root, map[string]string{"proc/stat": procStat1, "proc/diskstats": diskstats1, "proc/net/dev": netdev1})
	s := h.Collect(time.Unix(1120, 0))
	if s.CPU != nil || s.DiskReadBps != nil || s.NetRxBps != nil || s.NetTxBps != nil {
		t.Fatalf("backwards counters produced rates: %+v", s)
	}
	// The next interval measures again from the reset values.
	write(t, root, map[string]string{"proc/stat": procStat2})
	if s := h.Collect(time.Unix(1180, 0)); s.CPU == nil || *s.CPU != 50 {
		t.Fatalf("cpu after reset = %v", s.CPU)
	}
}

func TestHostWithoutSensorsOrFiles(t *testing.T) {
	h := NewHost(t.TempDir(), "/")
	h.statFS = fakeStatFS(nil)
	s := h.Collect(time.Unix(1000, 0))
	if s.Temp != nil || s.Temps != nil {
		t.Fatalf("no hwmon must mean no temperature: %+v", s)
	}
	if s.MemTotal != 0 || s.Disks == nil {
		t.Fatalf("empty root sample = %+v", s)
	}
	h.Collect(time.Unix(1060, 0))
}

func TestParseMeminfoWithoutMemAvailable(t *testing.T) {
	m, err := parseMeminfo([]byte("MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 50 kB\nCached: 250 kB\n"))
	if err != nil || m.available != 400*1024 || m.swapTotal != 0 {
		t.Fatalf("meminfo = %+v, %v", m, err)
	}
	if _, err := parseMeminfo([]byte("garbage")); err == nil {
		t.Fatal("meminfo without MemTotal accepted")
	}
}

func TestParseProcStatErrors(t *testing.T) {
	if _, err := parseProcStat([]byte("intr 1\n")); err == nil {
		t.Fatal("missing cpu line accepted")
	}
	if _, err := parseProcStat([]byte("cpu  x 0 0 0 0\n")); err == nil {
		t.Fatal("non-numeric field accepted")
	}
}

func TestParseLoadavgShort(t *testing.T) {
	if _, _, _, err := parseLoadavg([]byte("0.1 0.2")); err == nil {
		t.Fatal("short loadavg accepted")
	}
}
```

- [ ] **Run them, expect FAIL**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/sysmetrics/
```
Expected: `undefined: NewHost` (and the parsers).

- [ ] **Implement**

`internal/infra/sysmetrics/procfs.go`:

```go
// Package sysmetrics reads host and container resource usage straight from
// procfs, sysfs and cgroupfs: file reads only, no process forks, so it is
// cheap enough to run every minute forever on a small box. Every path is
// resolved under an injectable root ("/" in production) so tests run against
// fixture trees.
package sysmetrics

import (
	"bufio"
	"bytes"
	"errors"
	"strconv"
	"strings"
)

// cpuTimes is the aggregate line of /proc/stat in USER_HZ ticks.
type cpuTimes struct {
	total, idle uint64
}

// parseProcStat reads the aggregate "cpu" line. total is user+nice+system+
// idle+iowait+irq+softirq+steal (guest time is already inside user); idle
// includes iowait.
func parseProcStat(b []byte) (cpuTimes, error) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		var vals [8]uint64
		for i := 0; i < 8 && i+1 < len(fields); i++ {
			v, err := strconv.ParseUint(fields[i+1], 10, 64)
			if err != nil {
				return cpuTimes{}, err
			}
			vals[i] = v
		}
		var t cpuTimes
		for _, v := range vals {
			t.total += v
		}
		t.idle = vals[3] + vals[4]
		return t, nil
	}
	return cpuTimes{}, errors.New("proc/stat: no aggregate cpu line")
}

// memInfo is /proc/meminfo in bytes.
type memInfo struct {
	total, available, swapTotal, swapFree uint64
}

// parseMeminfo reads the fields the sample needs. Kernels older than 3.14
// lack MemAvailable; MemFree+Buffers+Cached stands in for it there.
func parseMeminfo(b []byte) (memInfo, error) {
	kv := map[string]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		if len(fields) > 1 && fields[1] == "kB" {
			v *= 1024
		}
		kv[key] = v
	}
	total, ok := kv["MemTotal"]
	if !ok {
		return memInfo{}, errors.New("meminfo: no MemTotal")
	}
	avail, ok := kv["MemAvailable"]
	if !ok {
		avail = kv["MemFree"] + kv["Buffers"] + kv["Cached"]
	}
	if avail > total {
		avail = total
	}
	free := kv["SwapFree"]
	if free > kv["SwapTotal"] {
		free = kv["SwapTotal"]
	}
	return memInfo{total: total, available: avail, swapTotal: kv["SwapTotal"], swapFree: free}, nil
}

// parseLoadavg reads the three load averages.
func parseLoadavg(b []byte) (l1, l5, l15 float64, err error) {
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return 0, 0, 0, errors.New("loadavg: short line")
	}
	var out [3]float64
	for i := range out {
		if out[i], err = strconv.ParseFloat(f[i], 64); err != nil {
			return 0, 0, 0, err
		}
	}
	return out[0], out[1], out[2], nil
}

// sectorSize is the unit of /proc/diskstats sector counts, fixed by the
// kernel regardless of the device's real sector size.
const sectorSize = 512

// parseDiskstats sums sectors read and written over the devices whole()
// accepts and returns bytes.
func parseDiskstats(b []byte, whole func(name string) bool) (read, written uint64) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || !whole(f[2]) {
			continue
		}
		r, err1 := strconv.ParseUint(f[5], 10, 64)
		w, err2 := strconv.ParseUint(f[9], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		read += r * sectorSize
		written += w * sectorSize
	}
	return read, written
}

// virtualDisk reports device names that are never physical disks.
func virtualDisk(name string) bool {
	for _, p := range []string{"loop", "ram", "dm-", "zram"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// netDev is one interface's byte counters from a net/dev file.
type netDev struct {
	name   string
	rx, tx uint64
}

// parseNetDev reads every interface line of a /proc/<pid>/net/dev file.
func parseNetDev(b []byte) []netDev {
	var out []netDev
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(f[0], 10, 64)
		tx, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, netDev{name: strings.TrimSpace(name), rx: rx, tx: tx})
	}
	return out
}

// hostInterface reports whether a host interface counts toward host network
// traffic: loopback and Docker's virtual plumbing would double-count
// container traffic.
func hostInterface(name string) bool {
	if name == "lo" {
		return false
	}
	for _, p := range []string{"veth", "docker", "br-"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

// hostNetns reports whether a container's net/dev shows the host's network
// namespace (network_mode: host): only the host has Docker's bridge and veth
// ends. The agent cannot readlink another user's /proc/<pid>/ns/net, so the
// interface names are the signal.
func hostNetns(devs []netDev) bool {
	for _, d := range devs {
		if d.name == "docker0" || strings.HasPrefix(d.name, "veth") || strings.HasPrefix(d.name, "br-") {
			return true
		}
	}
	return false
}

// sumNet adds the counters of the interfaces keep accepts.
func sumNet(devs []netDev, keep func(string) bool) (rx, tx uint64) {
	for _, d := range devs {
		if keep(d.name) {
			rx += d.rx
			tx += d.tx
		}
	}
	return rx, tx
}
```

`internal/infra/sysmetrics/hwmon.go`:

```go
package sysmetrics

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// readHwmon reads every temp*_input under <root>/sys/class/hwmon, in °C,
// labelled by temp*_label or else the chip's name; a repeated label gets a
// " #2", " #3" suffix. Readings outside (0, 150) °C are sensor noise and
// dropped. ok is false when no sensor reads.
func readHwmon(root string) (temps map[string]float64, hottest float64, ok bool) {
	base := filepath.Join(root, "sys", "class", "hwmon")
	chips, err := os.ReadDir(base)
	if err != nil {
		return nil, 0, false
	}
	sort.Slice(chips, func(i, j int) bool { return chips[i].Name() < chips[j].Name() })
	temps = map[string]float64{}
	for _, chip := range chips {
		dir := filepath.Join(base, chip.Name())
		name := readTrim(filepath.Join(dir, "name"))
		inputs, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		sort.Strings(inputs)
		for _, in := range inputs {
			raw := readTrim(in)
			milli, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				continue
			}
			c := milli / 1000
			if c <= 0 || c >= 150 {
				continue
			}
			label := readTrim(strings.TrimSuffix(in, "_input") + "_label")
			if label == "" {
				label = name
			}
			if label == "" {
				label = chip.Name()
			}
			key := label
			for n := 2; ; n++ {
				if _, dup := temps[key]; !dup {
					break
				}
				key = label + " #" + strconv.Itoa(n)
			}
			temps[key] = c
			if !ok || c > hottest {
				hottest = c
			}
			ok = true
		}
	}
	if !ok {
		return nil, 0, false
	}
	return temps, hottest, true
}

// readTrim returns a small file's trimmed content, "" when unreadable.
func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
```

`internal/infra/sysmetrics/statfs_linux.go`:

```go
//go:build linux

package sysmetrics

import "syscall"

// statFS reports the filesystem holding path: its device (to dedupe two
// paths on one filesystem) and used/total bytes as df counts them.
func statFS(path string) (fsInfo, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return fsInfo{}, err
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return fsInfo{}, err
	}
	bsize := uint64(fs.Bsize)
	total := fs.Blocks * bsize
	used := (fs.Blocks - fs.Bfree) * bsize
	return fsInfo{dev: uint64(st.Dev), used: used, total: total}, nil
}
```

`internal/infra/sysmetrics/statfs_other.go`:

```go
//go:build !linux

package sysmetrics

import "errors"

// statFS is Linux-only; elsewhere the sample simply has no disks.
func statFS(string) (fsInfo, error) { return fsInfo{}, errors.New("statfs: unsupported platform") }
```

`internal/infra/sysmetrics/host.go`:

```go
package sysmetrics

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"winterflow/internal/domain/metrics"
)

// fsInfo is one filesystem from statfs.
type fsInfo struct {
	dev         uint64
	used, total uint64
}

// hostCounters are the cumulative counters rates are computed from.
type hostCounters struct {
	at                         time.Time
	cpu                        cpuTimes
	diskRead, diskWrite        uint64
	netRx, netTx               uint64
	haveCPU, haveDisk, haveNet bool
}

// Host collects the host part of a sample. Not safe for concurrent use: one
// reporter goroutine owns it.
type Host struct {
	root   string
	mounts []string
	statFS func(path string) (fsInfo, error)
	prev   *hostCounters
}

// NewHost reads procfs/sysfs under root and measures the filesystems of the
// given mount paths (the root filesystem and the agent's data dir).
func NewHost(root string, mounts ...string) *Host {
	return &Host{root: root, mounts: mounts, statFS: statFS}
}

func (h *Host) read(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(h.root, rel))
}

// Collect takes one host sample. Rates compare against the previous call;
// the first call, and any counter that went backwards (reboot, wrap, a
// device vanishing), leaves that rate nil for this interval.
func (h *Host) Collect(now time.Time) metrics.ServerSample {
	var s metrics.ServerSample
	cur := hostCounters{at: now}

	if b, err := h.read("proc/stat"); err == nil {
		if t, err := parseProcStat(b); err == nil {
			cur.cpu, cur.haveCPU = t, true
		}
	}
	if b, err := h.read("proc/meminfo"); err == nil {
		if m, err := parseMeminfo(b); err == nil {
			s.MemTotal = float64(m.total)
			s.MemUsed = float64(m.total - m.available)
			s.SwapTotal = float64(m.swapTotal)
			s.SwapUsed = float64(m.swapTotal - m.swapFree)
		}
	}
	if b, err := h.read("proc/loadavg"); err == nil {
		s.Load1, s.Load5, s.Load15, _ = parseLoadavg(b)
	}
	if b, err := h.read("proc/diskstats"); err == nil {
		cur.diskRead, cur.diskWrite = parseDiskstats(b, h.wholeDisk)
		cur.haveDisk = true
	}
	if b, err := h.read("proc/net/dev"); err == nil {
		cur.netRx, cur.netTx = sumNet(parseNetDev(b), hostInterface)
		cur.haveNet = true
	}
	s.Disks = h.disks()
	if temps, hottest, ok := readHwmon(h.root); ok {
		s.Temps = temps
		s.Temp = &hottest
	}

	if p := h.prev; p != nil {
		secs := now.Sub(p.at).Seconds()
		if secs > 0 {
			if cur.haveCPU && p.haveCPU && cur.cpu.total > p.cpu.total && cur.cpu.idle >= p.cpu.idle {
				dt := float64(cur.cpu.total - p.cpu.total)
				busy := dt - float64(cur.cpu.idle-p.cpu.idle)
				if busy < 0 {
					busy = 0
				}
				pct := 100 * busy / dt
				s.CPU = &pct
			}
			if cur.haveDisk && p.haveDisk {
				s.DiskReadBps = rate(p.diskRead, cur.diskRead, secs)
				s.DiskWriteBps = rate(p.diskWrite, cur.diskWrite, secs)
			}
			if cur.haveNet && p.haveNet {
				s.NetRxBps = rate(p.netRx, cur.netRx, secs)
				s.NetTxBps = rate(p.netTx, cur.netTx, secs)
			}
		}
	}
	h.prev = &cur
	return s
}

// rate is (cur-prev)/secs, or nil when the counter went backwards.
func rate(prev, cur uint64, secs float64) *float64 {
	if cur < prev || secs <= 0 {
		return nil
	}
	r := float64(cur-prev) / secs
	return &r
}

// wholeDisk accepts physical whole disks: listed in /sys/block (partitions
// are not) and not a loop/ram/dm/zram device. diskstats writes "/" in names
// that sysfs spells "!" (cciss/c0d0).
func (h *Host) wholeDisk(name string) bool {
	if virtualDisk(name) {
		return false
	}
	_, err := os.Stat(filepath.Join(h.root, "sys", "block", strings.ReplaceAll(name, "/", "!")))
	return err == nil
}

// disks measures the configured mount paths, one entry per filesystem.
func (h *Host) disks() []metrics.Disk {
	out := []metrics.Disk{}
	seen := map[uint64]bool{}
	for _, m := range h.mounts {
		fi, err := h.statFS(m)
		if err != nil || fi.total == 0 || seen[fi.dev] {
			continue
		}
		seen[fi.dev] = true
		out = append(out, metrics.Disk{Mount: m, Used: float64(fi.used), Total: float64(fi.total)})
	}
	return out
}
```

- [ ] **Run them, expect PASS, and cross-compile** (the release targets 32-bit ARM, where `Statfs_t.Bsize` is `int32`)

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/sysmetrics/ && GOARCH=arm GOTMPDIR=$HOME/.cache/wf-gotmp go vet ./internal/infra/sysmetrics/ && GOOS=darwin GOTMPDIR=$HOME/.cache/wf-gotmp go vet ./internal/infra/sysmetrics/
```
Expected: `ok` and no vet output.

- [ ] **Commit**

```bash
git add internal/infra/sysmetrics
git commit -m "feat(agent): read host metrics from procfs and sysfs" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 4: Container collector (cgroup v2 systemd/cgroupfs, v1 fallback, per-netns net)

**Files:**
- Create: `internal/infra/sysmetrics/cgroup.go`
- Test: `internal/infra/sysmetrics/cgroup_test.go`

**Interfaces:**
- Consumes: `parseNetDev`, `hostNetns`, `sumNet`, `rate`, `readTrim`, `tree`/`write`/`netdevHeader` test helpers (Task 3).
- Produces: `type ContainerStat struct { CPU *float64; MemUsed float64; MemLimit *float64; NetRx, NetTx *float64 }`; `func NewContainers(root string, ncpu int) *Containers`; `func (c *Containers) Collect(now time.Time, ids []string) map[string]ContainerStat`. The result holds only resolved ids, and delta state for ids that were not passed is pruned.

- [ ] **Write the failing test**

`internal/infra/sysmetrics/cgroup_test.go`:

```go
package sysmetrics

import (
	"testing"
	"time"
)

const (
	ctrA = "aaaa1111"
	ctrB = "bbbb2222"
)

func netdev(rx, tx string) string {
	return netdevHeader +
		"    lo: 999 1 0 0 0 0 0 0 999 1 0 0 0 0 0 0\n" +
		"  eth0: " + rx + " 1 0 0 0 0 0 0 " + tx + " 1 0 0 0 0 0 0\n"
}

// v2 systemd driver: the dev host's layout.
func v2Systemd(usage, rx, tx string) map[string]string {
	dir := "sys/fs/cgroup/system.slice/docker-" + ctrA + ".scope/"
	return map[string]string{
		dir + "cpu.stat":       "usage_usec " + usage + "\nuser_usec 1\nsystem_usec 1\n",
		dir + "memory.current": "1040384\n",
		dir + "memory.stat":    "anon 1\nfile 40960\nactive_file 0\ninactive_file 40960\n",
		dir + "memory.max":     "max\n",
		dir + "cgroup.procs":   "378136\n378310\n",
		"proc/378136/net/dev":  netdev(rx, tx),
	}
}

func TestContainersV2SystemdDriver(t *testing.T) {
	root := tree(t, v2Systemd("1000000", "1000", "2000"))
	c := NewContainers(root, 4)
	got := c.Collect(time.Unix(1000, 0), []string{ctrA})
	st, ok := got[ctrA]
	if !ok {
		t.Fatal("v2 systemd layout not resolved")
	}
	if st.CPU != nil || st.NetRx != nil {
		t.Fatalf("first reading has rates: %+v", st)
	}
	if st.MemUsed != 1040384-40960 || st.MemLimit != nil {
		t.Fatalf("mem = %v limit = %v", st.MemUsed, st.MemLimit)
	}
	// +60 s of CPU time over 60 s on 4 cores = 25 % of the host.
	write(t, root, v2Systemd("61000000", "7000", "14000"))
	st = c.Collect(time.Unix(1060, 0), []string{ctrA})[ctrA]
	if st.CPU == nil || *st.CPU != 25 {
		t.Fatalf("cpu = %v", st.CPU)
	}
	if st.NetRx == nil || *st.NetRx != 100 || *st.NetTx != 200 {
		t.Fatalf("net = %v %v (lo excluded)", st.NetRx, st.NetTx)
	}
}

func TestContainersV2CgroupfsDriverWithLimit(t *testing.T) {
	dir := "sys/fs/cgroup/docker/" + ctrB + "/"
	root := tree(t, map[string]string{
		dir + "cpu.stat":       "usage_usec 500\n",
		dir + "memory.current": "2000\n",
		dir + "memory.stat":    "inactive_file 500\n",
		dir + "memory.max":     "268435456\n",
	})
	st, ok := NewContainers(root, 2).Collect(time.Unix(1000, 0), []string{ctrB})[ctrB]
	if !ok || st.MemUsed != 1500 || st.MemLimit == nil || *st.MemLimit != 268435456 {
		t.Fatalf("cgroupfs stat = %+v, %v", st, ok)
	}
	if st.NetRx != nil {
		t.Fatal("no cgroup.procs must mean no net")
	}
}

func TestContainersV1Fallback(t *testing.T) {
	sub := "docker/" + ctrA + "/"
	root := tree(t, map[string]string{
		"sys/fs/cgroup/cpu,cpuacct/" + sub + "cpuacct.usage":    "2000000000\n",
		"sys/fs/cgroup/memory/" + sub + "memory.usage_in_bytes": "5000\n",
		"sys/fs/cgroup/memory/" + sub + "memory.stat":           "cache 9\ntotal_inactive_file 1000\n",
		"sys/fs/cgroup/memory/" + sub + "memory.limit_in_bytes": "9223372036854771712\n",
	})
	c := NewContainers(root, 1)
	st, ok := c.Collect(time.Unix(1000, 0), []string{ctrA})[ctrA]
	if !ok || st.MemUsed != 4000 || st.MemLimit != nil {
		t.Fatalf("v1 stat = %+v, %v (unlimited must be nil)", st, ok)
	}
	write(t, root, map[string]string{"sys/fs/cgroup/cpu,cpuacct/" + sub + "cpuacct.usage": "32000000000\n"})
	st = c.Collect(time.Unix(1060, 0), []string{ctrA})[ctrA]
	if st.CPU == nil || *st.CPU != 50 { // 30 s of CPU in 60 s on one core
		t.Fatalf("v1 cpu = %v", st.CPU)
	}
}

func TestContainersRestartResetsCounters(t *testing.T) {
	root := tree(t, v2Systemd("90000000", "50000", "50000"))
	c := NewContainers(root, 1)
	c.Collect(time.Unix(1000, 0), []string{ctrA})
	// Restarted: a fresh cgroup (usage starts over) and a new pid/netns.
	files := v2Systemd("1000", "10", "10")
	files["sys/fs/cgroup/system.slice/docker-"+ctrA+".scope/cgroup.procs"] = "400000\n"
	files["proc/400000/net/dev"] = netdev("10", "10")
	write(t, root, files)
	st := c.Collect(time.Unix(1060, 0), []string{ctrA})[ctrA]
	if st.CPU != nil || st.NetRx != nil || st.NetTx != nil {
		t.Fatalf("restart produced rates: %+v", st)
	}
	files = v2Systemd("6001000", "6010", "6010")
	files["sys/fs/cgroup/system.slice/docker-"+ctrA+".scope/cgroup.procs"] = "400000\n"
	files["proc/400000/net/dev"] = netdev("6010", "6010")
	write(t, root, files)
	st = c.Collect(time.Unix(1120, 0), []string{ctrA})[ctrA]
	if st.CPU == nil || *st.CPU != 10 || st.NetRx == nil || *st.NetRx != 100 {
		t.Fatalf("after restart = %+v", st)
	}
}

func TestContainersHostNetworkHasNoNet(t *testing.T) {
	files := v2Systemd("1", "1", "1")
	files["proc/378136/net/dev"] = netdevHeader +
		"  eth0: 1 1 0 0 0 0 0 0 1 1 0 0 0 0 0 0\n" +
		"docker0: 1 1 0 0 0 0 0 0 1 1 0 0 0 0 0 0\n"
	root := tree(t, files)
	c := NewContainers(root, 1)
	c.Collect(time.Unix(1000, 0), []string{ctrA})
	st := c.Collect(time.Unix(1060, 0), []string{ctrA})[ctrA]
	if st.NetRx != nil || st.NetTx != nil {
		t.Fatalf("host-netns container reported net: %+v", st)
	}
}

func TestContainersUnknownLayoutAndPruning(t *testing.T) {
	root := tree(t, v2Systemd("1", "1", "1"))
	c := NewContainers(root, 1)
	got := c.Collect(time.Unix(1000, 0), []string{ctrA, "unknown"})
	if _, ok := got["unknown"]; ok || len(got) != 1 {
		t.Fatalf("unresolvable container reported: %v", got)
	}
	c.Collect(time.Unix(1060, 0), nil)
	if len(c.prev) != 0 {
		t.Fatalf("delta state kept for vanished containers: %v", c.prev)
	}
}
```

- [ ] **Run it, expect FAIL**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/sysmetrics/ -run Containers
```
Expected: `undefined: NewContainers`.

- [ ] **Implement**

`internal/infra/sysmetrics/cgroup.go`:

```go
package sysmetrics

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// cgroupLayout is where one container's cgroup files live.
type cgroupLayout struct {
	v2     bool
	cpuDir string // v2: the unified dir; v1: the cpuacct dir
	memDir string // v2: same as cpuDir; v1: the memory dir
}

// unlimitedV1 is the smallest memory.limit_in_bytes treated as "no limit":
// v1 reports an unlimited cgroup as a page-rounded 2^63-1.
const unlimitedV1 = 1 << 62

// ContainerStat is one container's usage. CPU is percent of the whole host
// (100 = every core busy); nil on its first reading and after a counter
// reset (the container restarted). Net is nil when the container shares the
// host's network namespace or on its first reading.
type ContainerStat struct {
	CPU      *float64
	MemUsed  float64
	MemLimit *float64 // nil = unlimited
	NetRx    *float64
	NetTx    *float64
}

// containerPrev is the delta state kept per container id.
type containerPrev struct {
	layout  cgroupLayout
	at      time.Time
	cpuUsec uint64
	haveCPU bool
	pid     string
	rx, tx  uint64
	haveNet bool
}

// Containers reads per-container usage from cgroupfs. Not safe for
// concurrent use: one reporter goroutine owns it.
type Containers struct {
	root string
	ncpu int
	prev map[string]*containerPrev
}

// NewContainers reads cgroupfs and procfs under root; ncpu turns cgroup CPU
// time into a percentage of the host.
func NewContainers(root string, ncpu int) *Containers {
	if ncpu < 1 {
		ncpu = 1
	}
	return &Containers{root: root, ncpu: ncpu, prev: map[string]*containerPrev{}}
}

func (c *Containers) cg(parts ...string) string {
	return filepath.Join(append([]string{c.root, "sys", "fs", "cgroup"}, parts...)...)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// resolve finds the container's cgroup: v2 systemd driver
// (system.slice/docker-<id>.scope), v2 cgroupfs driver (docker/<id>), then
// the same two under the v1 cpuacct and memory hierarchies.
func (c *Containers) resolve(id string) (cgroupLayout, bool) {
	subs := [][]string{{"system.slice", "docker-" + id + ".scope"}, {"docker", id}}
	for _, sub := range subs {
		dir := c.cg(sub...)
		if exists(filepath.Join(dir, "cpu.stat")) && exists(filepath.Join(dir, "memory.current")) {
			return cgroupLayout{v2: true, cpuDir: dir, memDir: dir}, true
		}
	}
	for _, sub := range subs {
		mem := c.cg(append([]string{"memory"}, sub...)...)
		if !exists(filepath.Join(mem, "memory.usage_in_bytes")) {
			continue
		}
		for _, ctl := range []string{"cpuacct", "cpu,cpuacct"} {
			cpu := c.cg(append([]string{ctl}, sub...)...)
			if exists(filepath.Join(cpu, "cpuacct.usage")) {
				return cgroupLayout{cpuDir: cpu, memDir: mem}, true
			}
		}
	}
	return cgroupLayout{}, false
}

// Collect reads every container in ids. The result holds only the ids whose
// cgroup was found; a container missing from it (stopped, or an unknown
// layout) contributes nothing. Delta state of ids not passed is dropped.
func (c *Containers) Collect(now time.Time, ids []string) map[string]ContainerStat {
	out := make(map[string]ContainerStat, len(ids))
	keep := make(map[string]bool, len(ids))
	for _, id := range ids {
		keep[id] = true
		p := c.prev[id]
		if p == nil || !exists(p.layout.cpuDir) {
			layout, ok := c.resolve(id)
			if !ok {
				delete(c.prev, id)
				continue
			}
			p = &containerPrev{layout: layout}
			c.prev[id] = p
		}
		st, ok := c.read(now, p)
		if ok {
			out[id] = st
		}
	}
	for id := range c.prev {
		if !keep[id] {
			delete(c.prev, id)
		}
	}
	return out
}

// read takes one container reading and advances its delta state.
func (c *Containers) read(now time.Time, p *containerPrev) (ContainerStat, bool) {
	var st ContainerStat
	l := p.layout
	usage, okCPU := c.cpuUsec(l)
	mem, okMem := c.memory(l)
	if !okCPU && !okMem {
		return st, false
	}
	st.MemUsed = mem
	st.MemLimit = c.limit(l)
	secs := now.Sub(p.at).Seconds()
	if okCPU && p.haveCPU && usage >= p.cpuUsec && secs > 0 {
		pct := 100 * float64(usage-p.cpuUsec) / (secs * 1e6 * float64(c.ncpu))
		st.CPU = &pct
	}
	p.cpuUsec, p.haveCPU = usage, okCPU

	pid := firstPID(filepath.Join(l.memDir, "cgroup.procs"))
	var rx, tx uint64
	okNet := false
	if pid != "" {
		if b, err := os.ReadFile(filepath.Join(c.root, "proc", pid, "net", "dev")); err == nil {
			devs := parseNetDev(b)
			if !hostNetns(devs) {
				rx, tx = sumNet(devs, func(n string) bool { return n != "lo" })
				okNet = true
			}
		}
	}
	// A new pid is a new network namespace (restart): its counters start
	// over, so this interval has no rate.
	if okNet && p.haveNet && pid == p.pid && secs > 0 {
		st.NetRx = rate(p.rx, rx, secs)
		st.NetTx = rate(p.tx, tx, secs)
	}
	p.pid, p.rx, p.tx, p.haveNet = pid, rx, tx, okNet
	p.at = now
	return st, true
}

// cpuUsec is the container's cumulative CPU time in microseconds.
func (c *Containers) cpuUsec(l cgroupLayout) (uint64, bool) {
	if l.v2 {
		b, err := os.ReadFile(filepath.Join(l.cpuDir, "cpu.stat"))
		if err != nil {
			return 0, false
		}
		v, ok := statField(b, "usage_usec")
		return v, ok
	}
	ns, err := strconv.ParseUint(readTrim(filepath.Join(l.cpuDir, "cpuacct.usage")), 10, 64)
	if err != nil {
		return 0, false
	}
	return ns / 1000, true
}

// memory is usage minus inactive page cache, as docker stats reports it.
func (c *Containers) memory(l cgroupLayout) (float64, bool) {
	usageFile, statKey := "memory.current", "inactive_file"
	if !l.v2 {
		usageFile, statKey = "memory.usage_in_bytes", "total_inactive_file"
	}
	usage, err := strconv.ParseUint(readTrim(filepath.Join(l.memDir, usageFile)), 10, 64)
	if err != nil {
		return 0, false
	}
	if b, err := os.ReadFile(filepath.Join(l.memDir, "memory.stat")); err == nil {
		if inactive, ok := statField(b, statKey); ok && inactive <= usage {
			usage -= inactive
		}
	}
	return float64(usage), true
}

// limit is the memory limit, nil when unlimited.
func (c *Containers) limit(l cgroupLayout) *float64 {
	file := "memory.max"
	if !l.v2 {
		file = "memory.limit_in_bytes"
	}
	raw := readTrim(filepath.Join(l.memDir, file))
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || v >= unlimitedV1 { // "max" fails to parse
		return nil
	}
	f := float64(v)
	return &f
}

// statField reads "key value" from a cgroup stat file.
func statField(b []byte, key string) (uint64, bool) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), " ")
		if ok && k == key {
			n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// firstPID is the first process listed in a cgroup.procs file.
func firstPID(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(b), "\n")
	line = strings.TrimSpace(line)
	if _, err := strconv.Atoi(line); err != nil {
		return ""
	}
	return line
}
```

- [ ] **Run it, expect PASS**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/sysmetrics/
```
Expected: `ok  winterflow/internal/infra/sysmetrics`.

- [ ] **Commit**

```bash
git add internal/infra/sysmetrics/cgroup.go internal/infra/sysmetrics/cgroup_test.go
git commit -m "feat(agent): read per-container usage from cgroupfs" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 5: Hourly disk scan in the orchestrator

**Files:**
- Create: `internal/infra/orchestrator/docker_compose/diskscan.go`
- Test: `internal/infra/orchestrator/docker_compose/diskscan_test.go`

**Interfaces:**
- Consumes: `metrics.DiskScan`, `metrics.AppDisk`, `metrics.DockerDF` (Task 1); the existing package helpers `execOutput`, `labelValue`, `composeProjectLabel`, `wfProjectPrefix`, `(*Repository).appDataDir`, `newTestRepo`, `dockerCalls`.
- Produces: `func (r *Repository) ScanDisk(ctx context.Context) (metrics.DiskScan, error)` (it satisfies `agent.DiskScanner` in Task 6); `var diskScanTimeout = 5 * time.Minute`; `func parseHumanSize(string) (float64, bool)`; `func buildDiskScan(systemDF, map[string]float64) metrics.DiskScan`; `func dirSize(ctx, dir string) float64`.

- [ ] **Write the failing test** (fake `docker` scripts resolve `cat`/`sleep` before `PATH` is replaced)

`internal/infra/orchestrator/docker_compose/diskscan_test.go`:

```go
package dockercompose

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// systemDFOut is trimmed `docker system df -v --format json` output in the
// shape Docker 29 prints (sizes are human strings).
const systemDFOut = `{
 "Images":[
  {"ID":"sha256:aaa","Repository":"nginx","Tag":"latest","Containers":"1","Size":"200MB","SharedSize":"50MB","UniqueSize":"150MB"},
  {"ID":"sha256:bbb","Repository":"postgres","Tag":"16","Containers":"1","Size":"1.2GB","SharedSize":"50MB","UniqueSize":"1.15GB"},
  {"ID":"sha256:ccc","Repository":"<none>","Tag":"<none>","Containers":"0","Size":"N/A","SharedSize":"0B","UniqueSize":"300MB"}
 ],
 "Containers":[
  {"ID":"c1","Image":"nginx","Labels":"com.docker.compose.project=wf-app-1,com.docker.compose.service=web","Mounts":"wf-app-1_data,/srv/bind","Size":"4.1kB"},
  {"ID":"c2","Image":"postgres:16","Labels":"com.docker.compose.project=wf-app-1,com.docker.compose.service=db","Mounts":"shared_ext","Size":"0B"},
  {"ID":"c3","Image":"postgres:16","Labels":"com.docker.compose.project=wf-app-2","Mounts":"shared_ext,app2_ext","Size":"1kB"},
  {"ID":"c4","Image":"nginx","Labels":"com.docker.compose.project=other","Mounts":"other_vol","Size":"9GB"}
 ],
 "Volumes":[
  {"Name":"wf-app-1_data","Labels":"com.docker.compose.project=wf-app-1,com.docker.compose.volume=data","Links":"1","Size":"26.59MB"},
  {"Name":"shared_ext","Labels":"","Links":"2","Size":"1GB"},
  {"Name":"app2_ext","Labels":"","Links":"1","Size":"2kB"},
  {"Name":"orphan","Labels":"","Links":"0","Size":"5MB"},
  {"Name":"remote","Labels":"","Links":"0","Size":"N/A"}
 ],
 "BuildCache":[
  {"Size":"10MB","Shared":"false","InUse":"false"},
  {"Size":"20MB","Shared":"true","InUse":"false"},
  {"Size":"30MB","Shared":"false","InUse":"true"}
 ]
}`

func TestParseHumanSize(t *testing.T) {
	cases := map[string]float64{
		"0B": 0, "127B": 127, "4.1kB": 4100, "26.59MB": 26.59e6, "1.2GB": 1.2e9, "2TB": 2e12,
		"26.38GB (74%)": 26.38e9, " 12.29kB ": 12290, "1KiB": 1024, "3": 3,
	}
	for in, want := range cases {
		got, ok := parseHumanSize(in)
		if !ok || got != want {
			t.Errorf("%q = %v (%v), want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"N/A", "", "GB", "1.2XB", "-5MB"} {
		if got, ok := parseHumanSize(bad); ok || got != 0 {
			t.Errorf("%q = %v, %v; want 0, false", bad, got, ok)
		}
	}
}

func TestBuildDiskScan(t *testing.T) {
	var df systemDF
	if err := jsonUnmarshal(systemDFOut, &df); err != nil {
		t.Fatal(err)
	}
	scan := buildDiskScan(df, map[string]float64{"app-1": 1000, "app-3": 7})

	// app-1: dir 1000 + its compose volume 26.59MB + writable 4.1kB; the
	// shared external volume is mounted by two apps, so nobody owns it.
	if got, want := scan.Apps["app-1"].Disk, 1000+26.59e6+4100; got != want {
		t.Errorf("app-1 disk = %v, want %v", got, want)
	}
	// Images: nginx (200MB) + postgres (1.2GB), each counted once.
	if got := scan.Apps["app-1"].Image; got != 1.4e9 {
		t.Errorf("app-1 image = %v", got)
	}
	// app-2: the external volume only it mounts + writable 1kB.
	if got := scan.Apps["app-2"].Disk; got != 2000+1000 {
		t.Errorf("app-2 disk = %v", got)
	}
	if got := scan.Apps["app-3"].Disk; got != 7 {
		t.Errorf("app without containers = %v", got)
	}
	if _, ok := scan.Apps["other"]; ok {
		t.Error("a non-winterflow compose project became an app")
	}
	// Host totals: unique 1.6GB + shared 50MB once; volumes; all build cache.
	if got := scan.Docker.Images; got != 150e6+1.15e9+300e6+50e6 {
		t.Errorf("images = %v", got)
	}
	if got := scan.Docker.Volumes; got != 26.59e6+1e9+2000+5e6 {
		t.Errorf("volumes = %v", got)
	}
	if got := scan.Docker.BuildCache; got != 60e6 {
		t.Errorf("build cache = %v", got)
	}
	// Reclaimable: the unused image's unique bytes, the unlinked volume,
	// unshared idle build cache.
	if got := scan.Docker.Reclaimable; got != 300e6+5e6+10e6 {
		t.Errorf("reclaimable = %v", got)
	}
}

func TestDirSizeSkipsSymlinksAndUnreadable(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "a"), make([]byte, 100), 0o644))
	must(os.MkdirAll(filepath.Join(dir, "source", ".git"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "source", ".git", "pack"), make([]byte, 50), 0o644))
	big := filepath.Join(t.TempDir(), "big")
	must(os.WriteFile(big, make([]byte, 10000), 0o644))
	must(os.Symlink(big, filepath.Join(dir, "link")))
	locked := filepath.Join(dir, "locked")
	must(os.MkdirAll(locked, 0o755))
	must(os.WriteFile(filepath.Join(locked, "secret"), make([]byte, 7), 0o644))
	must(os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	got := dirSize(context.Background(), dir)
	if os.Geteuid() == 0 {
		got -= 7 // root reads the locked dir anyway
	}
	if got != 150 {
		t.Fatalf("dirSize = %v, want 150", got)
	}
}

func TestScanDiskRunsOneSystemDF(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	out := filepath.Join(bin, "df.json")
	if err := os.WriteFile(out, []byte(systemDFOut), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := exec.LookPath("cat") // resolved before PATH is replaced
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n" + cat + " " + out + "\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	r := newTestRepo(t)
	if err := os.MkdirAll(filepath.Join(r.cfg.GetAppsDataDir(), "app-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.cfg.GetAppsDataDir(), "app-1", "compose.yml"), make([]byte, 64), 0o644); err != nil {
		t.Fatal(err)
	}
	scan, err := r.ScanDisk(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := scan.Apps["app-1"].Disk; got != 64+26.59e6+4100 {
		t.Fatalf("app-1 disk = %v", got)
	}
	if calls := dockerCalls(t, log); len(calls) != 1 || calls[0] != "system df -v --format json" {
		t.Fatalf("docker calls = %q", calls)
	}
}

func TestScanDiskTimesOut(t *testing.T) {
	sleep, err := exec.LookPath("sleep") // resolved before PATH is replaced
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nexec "+sleep+" 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	old := diskScanTimeout
	diskScanTimeout = 200 * time.Millisecond
	t.Cleanup(func() { diskScanTimeout = old })

	start := time.Now()
	_, err = newTestRepo(t).ScanDisk(context.Background())
	if err == nil || !strings.Contains(err.Error(), "killed") {
		t.Fatalf("a hung docker must fail the scan by the deadline, got %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("scan took %v despite the deadline", time.Since(start))
	}
}

func TestScanDiskBadJSON(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\necho 'not json'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if _, err := newTestRepo(t).ScanDisk(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("bad json err = %v", err)
	}
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
```

- [ ] **Run it, expect FAIL**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/orchestrator/docker_compose/ -run "HumanSize|DiskScan|DirSize|ScanDisk"
```
Expected: `undefined: parseHumanSize`, `undefined: systemDF`.

- [ ] **Implement**

`internal/infra/orchestrator/docker_compose/diskscan.go`:

```go
package dockercompose

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"winterflow/internal/domain/metrics"
)

// diskScanTimeout bounds the hourly scan (a var so tests can shorten it). `docker system df -v` walks every
// volume (43 s measured on a dev box); a hung daemon must not stall the
// metrics reporter forever.
var diskScanTimeout = 5 * time.Minute

// systemDF mirrors `docker system df -v --format json` (one JSON object).
// Docker renders every size as a human string ("26.59MB", "0B", "N/A").
type systemDF struct {
	Images []struct {
		ID         string `json:"ID"`
		Repository string `json:"Repository"`
		Tag        string `json:"Tag"`
		Containers string `json:"Containers"`
		Size       string `json:"Size"`
		SharedSize string `json:"SharedSize"`
		UniqueSize string `json:"UniqueSize"`
	} `json:"Images"`
	Containers []struct {
		ID     string `json:"ID"`
		Image  string `json:"Image"`
		Labels string `json:"Labels"`
		Mounts string `json:"Mounts"`
		Size   string `json:"Size"`
	} `json:"Containers"`
	Volumes []struct {
		Name   string `json:"Name"`
		Labels string `json:"Labels"`
		Links  string `json:"Links"`
		Size   string `json:"Size"`
	} `json:"Volumes"`
	BuildCache []struct {
		Size   string `json:"Size"`
		Shared string `json:"Shared"`
		InUse  string `json:"InUse"`
	} `json:"BuildCache"`
}

// sizeUnits are docker's (go-units HumanSize) decimal units.
var sizeUnits = map[string]float64{
	"b": 1, "kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12, "pb": 1e15,
	"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40,
}

// parseHumanSize turns "1.2GB", "4.1kB", "0B" or "26.38GB (74%)" into bytes.
// "N/A", "" and anything unparsable are 0 with ok=false: an unknown size must
// not fail the scan.
func parseHumanSize(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i >= 0 {
		s = s[:i]
	}
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	if i == 0 {
		return 0, false
	}
	n, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0, false
	}
	unit := strings.ToLower(s[i:])
	if unit == "" {
		unit = "b"
	}
	mult, ok := sizeUnits[unit]
	if !ok {
		return 0, false
	}
	return n * mult, true
}

func size(s string) float64 {
	v, _ := parseHumanSize(s)
	return v
}

// appOfProject maps a compose project label to the app id it belongs to.
func appOfProject(project string) string {
	id, ok := strings.CutPrefix(project, wfProjectPrefix)
	if !ok {
		return ""
	}
	return id
}

// imageKey normalizes an image reference for matching a container's Image
// against the image list ("nginx" means "nginx:latest").
func imageKey(ref string) string {
	if strings.HasPrefix(ref, "sha256:") || strings.Contains(ref, "@") {
		return ref
	}
	last := ref[strings.LastIndexByte(ref, '/')+1:]
	if !strings.Contains(last, ":") {
		return ref + ":latest"
	}
	return ref
}

// buildDiskScan derives host totals and per-app usage from one system df.
// A volume belongs to an app when its compose project is wf-<appID>, else
// when exactly one app's containers mount it (an external volume from the
// volume registry). appDirs adds each app directory's size.
func buildDiskScan(df systemDF, appDirs map[string]float64) metrics.DiskScan {
	scan := metrics.DiskScan{Apps: map[string]metrics.AppDisk{}}
	add := func(app string, disk, image float64) {
		a := scan.Apps[app]
		a.Disk += disk
		a.Image += image
		scan.Apps[app] = a
	}
	for app, dir := range appDirs {
		add(app, dir, 0)
	}

	// Images: unique bytes plus each distinct shared size once (docker's
	// exact layer dedup is not in the -v output; this lands within a few
	// percent of `docker system df`). Unused images' unique bytes are
	// reclaimable.
	imageSize := map[string]float64{}
	shared := map[float64]bool{}
	for _, im := range df.Images {
		u := size(im.UniqueSize)
		scan.Docker.Images += u
		if sh := size(im.SharedSize); sh > 0 && !shared[sh] {
			shared[sh] = true
			scan.Docker.Images += sh
		}
		if strings.TrimSpace(im.Containers) == "0" {
			scan.Docker.Reclaimable += u
		}
		full := size(im.Size)
		imageSize[im.ID] = full
		if im.Repository != "" && im.Repository != "<none>" {
			imageSize[imageKey(im.Repository+":"+im.Tag)] = full
		}
	}

	// Containers: writable layers, image per app (each image once), and
	// which apps mount which volume.
	mountedBy := map[string]map[string]bool{}
	appImages := map[string]map[string]bool{}
	for _, c := range df.Containers {
		app := appOfProject(labelValue(c.Labels, composeProjectLabel))
		for _, m := range strings.Split(c.Mounts, ",") {
			if m = strings.TrimSpace(m); m == "" || app == "" {
				continue
			}
			if mountedBy[m] == nil {
				mountedBy[m] = map[string]bool{}
			}
			mountedBy[m][app] = true
		}
		if app == "" {
			continue
		}
		add(app, size(c.Size), 0)
		key := imageKey(c.Image)
		if appImages[app] == nil {
			appImages[app] = map[string]bool{}
		}
		if !appImages[app][key] {
			appImages[app][key] = true
			add(app, 0, imageSize[key])
		}
	}

	for _, v := range df.Volumes {
		s := size(v.Size)
		scan.Docker.Volumes += s
		if strings.TrimSpace(v.Links) == "0" {
			scan.Docker.Reclaimable += s
		}
		app := appOfProject(labelValue(v.Labels, composeProjectLabel))
		if app == "" && len(mountedBy[v.Name]) == 1 {
			for only := range mountedBy[v.Name] {
				app = only
			}
		}
		if app != "" {
			add(app, s, 0)
		}
	}

	for _, b := range df.BuildCache {
		s := size(b.Size)
		scan.Docker.BuildCache += s
		if b.Shared != "true" && b.InUse != "true" {
			scan.Docker.Reclaimable += s
		}
	}
	return scan
}

// dirSize sums the regular files under dir without following symlinks.
// Unreadable subtrees (root-owned bind-mount data) are skipped, not fatal.
func dirSize(ctx context.Context, dir string) float64 {
	var total float64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += float64(info.Size())
		}
		return nil
	})
	return total
}

// ScanDisk measures disk usage: one `docker system df -v` (bounded by
// diskScanTimeout) for images, volumes, build cache and writable layers,
// plus a walk of every app directory. It is hourly work; never call it from
// the 60 s path.
func (r *Repository) ScanDisk(ctx context.Context) (metrics.DiskScan, error) {
	ctx, cancel := context.WithTimeout(ctx, diskScanTimeout)
	defer cancel()
	out, err := execOutput(ctx, "docker", "system", "df", "-v", "--format", "json")
	if err != nil {
		return metrics.DiskScan{}, err
	}
	var df systemDF
	if err := json.Unmarshal(out, &df); err != nil {
		return metrics.DiskScan{}, err
	}
	dirs := map[string]float64{}
	entries, err := os.ReadDir(r.cfg.GetAppsDataDir())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return metrics.DiskScan{}, err
	}
	for _, e := range entries {
		if e.IsDir() {
			dirs[e.Name()] = dirSize(ctx, r.appDataDir(e.Name()))
		}
	}
	if err := ctx.Err(); err != nil {
		return metrics.DiskScan{}, err
	}
	return buildDiskScan(df, dirs), nil
}
```

- [ ] **Run it, expect PASS**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/orchestrator/docker_compose/
```
Expected: `ok` (the timeout test finishes in ~0.2 s).

- [ ] **Commit**

```bash
git add internal/infra/orchestrator/docker_compose/diskscan.go internal/infra/orchestrator/docker_compose/diskscan_test.go
git commit -m "feat(agent): measure per-app disk usage with an hourly docker system df" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 6: Agent metrics reporter, disk scanner loop and wiring

**Files:**
- Create: `internal/app/agent/metrics_reporter.go`
- Modify: `internal/infra/bootstrap/standalone.go`, `cmd/agent/main.go`
- Test: `internal/app/agent/metrics_reporter_test.go`

**Interfaces:**
- Consumes: `StatusSource`, `EventSink`, `sinkCall`/`collectSink` test helpers (existing `reporter.go`/`reporter_test.go`); `metrics.*` (Task 1); `sysmetrics.NewHost`, `sysmetrics.NewContainers`, `sysmetrics.ContainerStat` (Tasks 3–4); `(*dockercompose.Repository).ScanDisk` (Task 5); `cfg.IsMetricsEnabled`, `cfg.GetMetricsDiskInterval` (Task 2); `bus.EventServerMetrics`.
- Produces (package `winterflow/internal/app/agent`): `const MetricsInterval = 60 * time.Second`; `type StatusSnapshot` + `NewStatusSnapshot(StatusSource) *StatusSnapshot` + `GetAppsStatus(ctx)` + `Latest() ([]command.AppStatus, bool)`; `type HostCollector`, `type ContainerCollector`, `type DiskScanner` interfaces; `type MetricsReporter` + `NewMetricsReporter(HostCollector, ContainerCollector, *StatusSnapshot)` + `SetDiskScan(metrics.DiskScan)` + `Sample(now) metrics.Sample`; `func RunMetricsReporter(ctx, *MetricsReporter, EventSink, time.Duration, *logger.Logger)`; `func RunDiskScanner(ctx, DiskScanner, *MetricsReporter, time.Duration, *logger.Logger)`; `func StartMetrics(ctx, *config.ServerConfig, *StatusSnapshot, DiskScanner, EventSink, *logger.Logger) bool`.
- The status reporter now polls the `StatusSnapshot` wrapper, so the 60 s path never runs its own `docker ps`.

- [ ] **Write the failing test**

`internal/app/agent/metrics_reporter_test.go`:

```go
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
	fail  bool
}

func (f *fakeScanner) ScanDisk(context.Context) (metrics.DiskScan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail {
		return metrics.DiskScan{}, errors.New("df timed out")
	}
	return metrics.DiskScan{Docker: metrics.DockerDF{Volumes: float64(f.calls)}}, nil
}

func TestRunDiskScannerKeepsLastGoodScan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	m := NewMetricsReporter(&fakeHost{}, &fakeContainers{}, NewStatusSnapshot(&fakeStatus{}))
	sc := &fakeScanner{}
	go RunDiskScanner(ctx, sc, m, time.Hour, log)
	deadline := time.Now().Add(2 * time.Second)
	for m.diskScan() == nil {
		if time.Now().After(deadline) {
			t.Fatal("no immediate scan")
		}
		time.Sleep(5 * time.Millisecond)
	}
	sc.mu.Lock()
	sc.fail = true
	sc.mu.Unlock()
	if s := m.Sample(time.Unix(1, 0)); s.Server.DockerDF == nil || s.Server.DockerDF.Volumes != 1 {
		t.Fatalf("scan not attached: %+v", s.Server.DockerDF)
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
```

- [ ] **Run it, expect FAIL**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/app/agent/ -run "Sample|Snapshot|Metrics|DiskScanner|StartMetrics"
```
Expected: `undefined: buildSample`, `undefined: NewStatusSnapshot`.

- [ ] **Implement**

`internal/app/agent/metrics_reporter.go`:

```go
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

// GetAppsStatus queries the wrapped source and caches a successful answer.
func (s *StatusSnapshot) GetAppsStatus(ctx context.Context) ([]command.AppStatus, error) {
	apps, err := s.src.GetAppsStatus(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.apps, s.ok = apps, true
	s.mu.Unlock()
	return apps, nil
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
type ContainerCollector interface {
	Collect(now time.Time, ids []string) map[string]sysmetrics.ContainerStat
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
	return buildSample(now, host, apps, stats, m.diskScan())
}

// buildSample sums container stats per app and attaches the disk scan.
// An app whose running containers all failed to resolve is flagged
// containers_unavailable; mem_limit is summed only when every running
// container has one; rate fields stay nil when no container has a rate.
func buildSample(now time.Time, host metrics.ServerSample, apps []command.AppStatus, stats map[string]sysmetrics.ContainerStat, scan *metrics.DiskScan) metrics.Sample {
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
			s.ContainersUnavailable = true
		case resolved > 0:
			s.MemUsed = &mem
			if limited == live {
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
```

Wire both topologies through the snapshot (standalone keeps its claim-gated sink, and the agent keeps `SendEvent`):

Apply to `internal/infra/bootstrap/standalone.go`:

```diff
--- a/internal/infra/bootstrap/standalone.go
+++ b/internal/infra/bootstrap/standalone.go
@@ -70,13 +70,21 @@
 	// feeds the status cache (doubling as the liveness pulse) and the SSE push.
 	// Until the embedded server is claimed there is no server id to report as,
 	// so ticks are skipped silently.
-	go appagent.RunStatusReporter(ctx, orchestrator, func(kind bus.EventKind, payload []byte) error {
+	sink := func(kind bus.EventKind, payload []byte) error {
 		id, ok, err := serverRepo.FirstServerID(ctx)
 		if err != nil || !ok {
 			return err
 		}
 		return b.Publish(ctx, cfg.GetBusEventsQueue(), bus.EventMessage{ServerID: id, Kind: kind, Payload: payload})
-	}, 30*time.Second, log)
+	}
+	// The snapshot lets the metrics reporter reuse the status reporter's
+	// `docker ps` instead of running its own.
+	statusSnapshot := appagent.NewStatusSnapshot(orchestrator)
+	go appagent.RunStatusReporter(ctx, statusSnapshot, sink, 30*time.Second, log)
+
+	// Resource metrics (server.metrics every 60 s, disk scan hourly) through
+	// the same events path; off with METRICS_ENABLED=false.
+	appagent.StartMetrics(ctx, cfg, statusSnapshot, orchestrator, sink, log)
 
 	// Auto-update for git-sourced apps.
 	go appagent.RunSourcePoller(ctx, orchestrator, log)
```

Apply to `cmd/agent/main.go`:

```diff
--- a/cmd/agent/main.go
+++ b/cmd/agent/main.go
@@ -95,9 +95,16 @@
 
 	// Periodic apps-status push: feeds the API's status cache and the SSE
 	// live-status pipeline. Ticks while the stream is down are skipped.
-	go appagent.RunStatusReporter(ctx, orchestrator, func(kind bus.EventKind, payload []byte) error {
+	sink := func(kind bus.EventKind, payload []byte) error {
 		return agent.SendEvent(string(kind), payload)
-	}, 30*time.Second, log)
+	}
+	statusSnapshot := appagent.NewStatusSnapshot(orchestrator)
+	go appagent.RunStatusReporter(ctx, statusSnapshot, sink, 30*time.Second, log)
+
+	// Resource metrics: a server.metrics event every 60 s (procfs/cgroupfs
+	// reads, no forks) plus the hourly disk scan; off with
+	// METRICS_ENABLED=false.
+	appagent.StartMetrics(ctx, cfg, statusSnapshot, orchestrator, sink, log)
 
 	// Auto-update for git-sourced apps: polls upstreams on each app's own
 	// interval and redeploys on new commits.
```

- [ ] **Run it, expect PASS, and build every binary**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test -race ./internal/app/agent/ && GOTMPDIR=$HOME/.cache/wf-gotmp go build ./cmd/...
```
Expected: `ok  winterflow/internal/app/agent` and a clean build.

- [ ] **Commit**

```bash
git add internal/app/agent/metrics_reporter.go internal/app/agent/metrics_reporter_test.go internal/infra/bootstrap/standalone.go cmd/agent/main.go
git commit -m "feat(agent): send server.metrics every minute" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 7: metric_samples table, repository (insert, series, prune) and app-delete cleanup

**Files:**
- Create: `internal/infra/db/migrations/sqlite/0010_metrics.sql`, `internal/infra/db/migrations/pgsql/0010_metrics.sql`, `internal/infra/db/sqlc/query/sqlite/metrics.sql`, `internal/infra/db/sqlc/query/pgsql/metrics.sql`, `internal/domain/port/metric.go`, `internal/infra/db/repository/metric/repo.go`, `internal/infra/db/repository/metric/sqlite.go`, `internal/infra/db/repository/metric/pgsql.go`
- Modify: `internal/infra/db/sqlc/shapes_test.go`, `internal/infra/db/repository/app/repo.go`, `internal/infra/db/repository/app/sqlite.go`, `internal/infra/db/repository/app/pgsql.go`, `internal/infra/db/repository/timestamps_test.go`; regenerated `internal/infra/db/sqlc/gen/**`
- Test: `internal/infra/db/repository/metric/repo_test.go`, `internal/infra/db/repository/app/repo_test.go`

**Interfaces:**
- Consumes: `metrics.Values`, `metrics.Bucket`, `metrics.Tier*`, `metrics.Tiers` (Task 1); `backend.TxManager`, `types.DateTime`, `dbtest.*`; existing query `ListAppIDsByServer` (apps.sql).
- Produces: `port.MetricRepository` with `InsertSample(ctx, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values) error`, `Series(ctx, serverID, appID string, tier metrics.Tier, from time.Time) ([]metrics.Bucket, error)`, `AppsSeries(ctx, serverID string, tier metrics.Tier, from time.Time) (map[string][]metrics.Bucket, error)`, and `Prune(ctx, now time.Time) (int64, error)` (Task 8 adds `Rollup`). Also `metric.New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error)`, `(*Repo).insert(...)` and `(*Repo).hydrate(...)` (reused by Task 8), and queries `InsertMetricSample`, `ListMetricSamples`, `ListServerAppMetricSamples`, `DeleteMetricSamplesBefore :execrows`, `DeleteMetricSamplesByApp`. `AppRepository.DeleteApp` and `SyncApps` now delete the app's samples in the same transaction.

- [ ] **Write the failing tests** (the metric repo test, the app-delete test appended to `app/repo_test.go`, and the timestamp-format check that must write every TIMESTAMP column)

`internal/infra/db/repository/metric/repo_test.go`:

```go
package metric

import (
	"context"
	"testing"
	"time"

	"winterflow/internal/domain/metrics"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org1, org2 = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2 = dbtest.ID(11), dbtest.ID(12)
	appA, appB = dbtest.ID(21), dbtest.ID(22)
	appGone    = dbtest.ID(29) // never in apps
)

// t0 is a bucket-aligned instant for every tier (00:00 UTC).
var t0 = time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	dbtest.SeedApp(t, d, appA, srv1, "a")
	dbtest.SeedApp(t, d, appB, srv1, "b")
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func count(t *testing.T, d *db.DB, where string, args ...any) string {
	t.Helper()
	return dbtest.QueryString(t, d, "SELECT CAST(COUNT(*) AS TEXT) FROM metric_samples WHERE "+where, args...)
}

func mustInsert(t *testing.T, ctx context.Context, r *Repo, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values) {
	t.Helper()
	if err := r.InsertSample(ctx, serverID, at, server, apps); err != nil {
		t.Fatal(err)
	}
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestInsertSampleWritesServerAndKnownApps(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	at := t0.Add(5*time.Minute + 42*time.Second)
	mustInsert(t, ctx, repo, srv1, at, metrics.Values{"cpu": 12.5, "mem_used": 1e9},
		map[string]metrics.Values{appA: {"cpu": 3}, appGone: {"cpu": 9}})
	// The same minute again (replay, second API replica) is ignored.
	mustInsert(t, ctx, repo, srv1, at.Add(10*time.Second), metrics.Values{"cpu": 99}, nil)

	if got := count(t, d, "tier = 1"); got != "2" {
		t.Fatalf("rows = %s, want server + appA (unknown app skipped)", got)
	}
	got, err := repo.Series(ctx, srv1, "", metrics.Tier1, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Data["cpu"] != 12.5 || got[0].Samples != 1 ||
		!got[0].Start.Equal(t0.Add(5*time.Minute)) || got[0].Start.Location() != time.UTC {
		t.Fatalf("server series = %+v", got)
	}
	apps, err := repo.AppsSeries(ctx, srv1, metrics.Tier1, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || len(apps[appA]) != 1 || apps[appA][0].Data["cpu"] != 3 {
		t.Fatalf("apps series = %+v", apps)
	}
}

func TestSeriesFiltersByTargetTierAndTime(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	for i := 0; i < 5; i++ {
		mustInsert(t, ctx, repo, srv1, t0.Add(time.Duration(i)*time.Minute), metrics.Values{"cpu": float64(i)},
			map[string]metrics.Values{appA: {"cpu": float64(10 + i)}})
	}
	mustInsert(t, ctx, repo, srv2, t0, metrics.Values{"cpu": 77}, nil)

	got, err := repo.Series(ctx, srv1, "", metrics.Tier1, t0.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Data["cpu"] != 2 || got[2].Data["cpu"] != 4 {
		t.Fatalf("series = %+v", got)
	}
	app, err := repo.Series(ctx, srv1, appA, metrics.Tier1, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(app) != 5 || app[0].Data["cpu"] != 10 {
		t.Fatalf("app series = %+v", app)
	}
	if other, _ := repo.Series(ctx, srv2, "", metrics.Tier10, t0); len(other) != 0 {
		t.Fatalf("tier 10 before any rollup = %+v", other)
	}
	if empty, _ := repo.Series(ctx, srv1, appB, metrics.Tier1, t0); empty == nil || len(empty) != 0 {
		t.Fatalf("empty series = %#v, want non-nil empty", empty)
	}
}

func TestSeriesSkipsUndecodableRows(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	dbtest.Exec(t, d, `INSERT INTO metric_samples (server_id, app_id, tier, bucket_start, samples, data) VALUES (?, '', 1, ?, 1, 'not json')`,
		srv1, "2026-10-11 00:00:00")
	mustInsert(t, ctx, repo, srv1, t0.Add(time.Minute), metrics.Values{"cpu": 1}, nil)
	got, err := repo.Series(ctx, srv1, "", metrics.Tier1, t0)
	if err != nil || len(got) != 1 {
		t.Fatalf("series = %+v, %v", got, err)
	}
}

func TestPruneKeepsEachTiersRetention(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	now := t0.Add(40 * 24 * time.Hour)
	for _, tier := range metrics.Tiers {
		for _, age := range []time.Duration{tier.Retention() + tier.Duration(), tier.Retention() - tier.Duration()} {
			dbtest.Exec(t, d, `INSERT INTO metric_samples (server_id, app_id, tier, bucket_start, samples, data) VALUES (?, '', ?, ?, 1, '{}')`,
				srv1, int(tier), tier.BucketStart(now.Add(-age)).Format("2006-01-02 15:04:05"))
		}
	}
	n, err := repo.Prune(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(metrics.Tiers)) {
		t.Fatalf("pruned %d rows, want one per tier", n)
	}
	for _, tier := range metrics.Tiers {
		if got := count(t, d, "tier = ?", int(tier)); got != "1" {
			t.Fatalf("tier %d keeps %s rows, want the young one", tier, got)
		}
	}
}

func TestServerDeleteCascades(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustInsert(t, ctx, repo, srv2, t0, metrics.Values{"cpu": 1}, nil)
	dbtest.Exec(t, d, `DELETE FROM servers WHERE server_id = ?`, srv2)
	if got := count(t, d, "server_id = ?", srv2); got != "0" {
		t.Fatalf("%s rows survive their server", got)
	}
}
```

Append to `internal/infra/db/repository/app/repo_test.go`:

```go
// seedSample writes one tier-1 metric row straight into metric_samples.
func seedSample(t *testing.T, d *db.DB, serverID, appID string) {
	t.Helper()
	dbtest.Exec(t, d, `INSERT INTO metric_samples (server_id, app_id, tier, bucket_start, samples, data) VALUES (?, ?, 1, ?, 1, '{}')`,
		serverID, appID, "2026-10-11 00:00:00")
}

func samples(t *testing.T, d *db.DB, appID string) string {
	t.Helper()
	return dbtest.QueryString(t, d, `SELECT CAST(COUNT(*) AS TEXT) FROM metric_samples WHERE app_id = ?`, appID)
}

func TestDeleteAppRemovesItsMetricSamples(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	for _, id := range []string{appA, appB} {
		if err := repo.SaveApp(ctx, model.App{ID: id, ServerID: srv1, Name: id}); err != nil {
			t.Fatal(err)
		}
		seedSample(t, d, srv1, id)
	}
	seedSample(t, d, srv1, "") // the server's own row

	if err := repo.DeleteApp(ctx, appA); err != nil {
		t.Fatal(err)
	}
	if got := samples(t, d, appA); got != "0" {
		t.Fatalf("deleted app keeps %s samples", got)
	}
	if samples(t, d, appB) != "1" || samples(t, d, "") != "1" {
		t.Fatal("other targets' samples touched")
	}

	// A reconcile that no longer lists appB drops its samples too.
	if err := repo.SyncApps(ctx, srv1, nil); err != nil {
		t.Fatal(err)
	}
	if got := samples(t, d, appB); got != "0" {
		t.Fatalf("reconciled-away app keeps %s samples", got)
	}
}
```

Apply to `internal/infra/db/repository/timestamps_test.go`:

```diff
--- a/internal/infra/db/repository/timestamps_test.go
+++ b/internal/infra/db/repository/timestamps_test.go
@@ -7,12 +7,14 @@
 	"time"
 
 	"winterflow/internal/domain/dto"
+	"winterflow/internal/domain/metrics"
 	"winterflow/internal/domain/model"
 	"winterflow/internal/infra/db"
 	"winterflow/internal/infra/db/backend"
 	"winterflow/internal/infra/db/dbtest"
 	apprepo "winterflow/internal/infra/db/repository/app"
 	appdomainrepo "winterflow/internal/infra/db/repository/appdomain"
+	metricrepo "winterflow/internal/infra/db/repository/metric"
 	serverrepo "winterflow/internal/infra/db/repository/server"
 	serverdomainrepo "winterflow/internal/infra/db/repository/serverdomain"
 	userrepo "winterflow/internal/infra/db/repository/user"
@@ -41,6 +43,7 @@
 	apps := must(apprepo.New(d.Tx, d.Driver, dbtest.Log))
 	appDomains := must(appdomainrepo.New(d.Tx, d.Driver, dbtest.Log))
 	serverDomains := must(serverdomainrepo.New(d.Tx, d.Driver, dbtest.Log))
+	metricSamples := must(metricrepo.New(d.Tx, d.Driver, dbtest.Log))
 
 	admin := must(users.BootstrapLocalAdmin(ctx, "Admin", "admin@example.com", "correct horse battery"))
 	orgID := must(users.PrimaryOrganizationID(ctx, admin.ID))
@@ -94,6 +97,10 @@
 		t.Fatal(err)
 	}
 
+	if err := metricSamples.InsertSample(ctx, srv.ID, at, metrics.Values{"cpu": 1}, nil); err != nil {
+		t.Fatal(err)
+	}
+
 	columns := timestampColumns(t, d)
 	if len(columns) == 0 {
 		t.Fatal("found no TIMESTAMP columns")
```

- [ ] **Run them, expect FAIL**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/db/repository/...
```
Expected: `cannot find package .../repository/metric` and, in `app`, `no such table: metric_samples`.

- [ ] **Add the migration** (identical except the `server_id` type, which matches its FK target)

`internal/infra/db/migrations/sqlite/0010_metrics.sql`:

```sql
-- Monitoring samples: one row per (server, app or '' for the server itself,
-- tier in minutes, bucket). data is a JSON object {field: avg, field_max: max}
-- (tier 1 holds plain values). Rolled up 1 -> 10 -> 20 -> 120 -> 480 and
-- pruned per tier by the API; bounded by retention, never by row count.

CREATE TABLE metric_samples (
    server_id VARCHAR(36) NOT NULL,
    app_id VARCHAR(36) NOT NULL DEFAULT '',
    tier INTEGER NOT NULL,
    bucket_start TIMESTAMP NOT NULL,
    samples INTEGER NOT NULL,
    data TEXT NOT NULL,
    PRIMARY KEY (server_id, app_id, tier, bucket_start),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE INDEX idx_metric_samples_tier_bucket ON metric_samples (tier, bucket_start);
```

`internal/infra/db/migrations/pgsql/0010_metrics.sql`:

```sql
-- Monitoring samples: one row per (server, app or '' for the server itself,
-- tier in minutes, bucket). data is a JSON object {field: avg, field_max: max}
-- (tier 1 holds plain values). Rolled up 1 -> 10 -> 20 -> 120 -> 480 and
-- pruned per tier by the API; bounded by retention, never by row count.

CREATE TABLE metric_samples (
    server_id UUID NOT NULL,
    app_id VARCHAR(36) NOT NULL DEFAULT '',
    tier INTEGER NOT NULL,
    bucket_start TIMESTAMP NOT NULL,
    samples INTEGER NOT NULL,
    data TEXT NOT NULL,
    PRIMARY KEY (server_id, app_id, tier, bucket_start),
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE INDEX idx_metric_samples_tier_bucket ON metric_samples (tier, bucket_start);
```

- [ ] **Add the queries and regenerate**

`internal/infra/db/sqlc/query/sqlite/metrics.sql`:

```sql
-- metric_samples: monitoring time series (see migration 0010).

-- name: InsertMetricSample :exec
INSERT INTO metric_samples (server_id, app_id, tier, bucket_start, samples, data)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (server_id, app_id, tier, bucket_start) DO NOTHING;

-- name: ListMetricSamples :many
SELECT * FROM metric_samples
WHERE server_id = ? AND app_id = ? AND tier = ? AND bucket_start >= ?
ORDER BY bucket_start ASC;

-- name: ListServerAppMetricSamples :many
SELECT * FROM metric_samples
WHERE server_id = ? AND app_id <> '' AND tier = ? AND bucket_start >= ?
ORDER BY app_id ASC, bucket_start ASC;

-- name: DeleteMetricSamplesBefore :execrows
DELETE FROM metric_samples WHERE tier = ? AND bucket_start < ?;

-- name: DeleteMetricSamplesByApp :exec
DELETE FROM metric_samples WHERE app_id = ?;
```

`internal/infra/db/sqlc/query/pgsql/metrics.sql`:

```sql
-- metric_samples: monitoring time series (see migration 0010).

-- name: InsertMetricSample :exec
INSERT INTO metric_samples (server_id, app_id, tier, bucket_start, samples, data)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (server_id, app_id, tier, bucket_start) DO NOTHING;

-- name: ListMetricSamples :many
SELECT * FROM metric_samples
WHERE server_id = $1 AND app_id = $2 AND tier = $3 AND bucket_start >= $4
ORDER BY bucket_start ASC;

-- name: ListServerAppMetricSamples :many
SELECT * FROM metric_samples
WHERE server_id = $1 AND app_id <> '' AND tier = $2 AND bucket_start >= $3
ORDER BY app_id ASC, bucket_start ASC;

-- name: DeleteMetricSamplesBefore :execrows
DELETE FROM metric_samples WHERE tier = $1 AND bucket_start < $2;

-- name: DeleteMetricSamplesByApp :exec
DELETE FROM metric_samples WHERE app_id = $1;
```

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp make sqlc
```

Add the new type pairs at the top of the `var (` block in `internal/infra/db/sqlc/shapes_test.go`:

```go
	_ = sqlitegen.MetricSample(pgsqlgen.MetricSample{})
	_ = pgsqlgen.MetricSample(sqlitegen.MetricSample{})
	_ = sqlitegen.InsertMetricSampleParams(pgsqlgen.InsertMetricSampleParams{})
	_ = pgsqlgen.InsertMetricSampleParams(sqlitegen.InsertMetricSampleParams{})
	_ = sqlitegen.ListMetricSamplesParams(pgsqlgen.ListMetricSamplesParams{})
	_ = pgsqlgen.ListMetricSamplesParams(sqlitegen.ListMetricSamplesParams{})
	_ = sqlitegen.ListServerAppMetricSamplesParams(pgsqlgen.ListServerAppMetricSamplesParams{})
	_ = pgsqlgen.ListServerAppMetricSamplesParams(sqlitegen.ListServerAppMetricSamplesParams{})
	_ = sqlitegen.DeleteMetricSamplesBeforeParams(pgsqlgen.DeleteMetricSamplesBeforeParams{})
	_ = pgsqlgen.DeleteMetricSamplesBeforeParams(sqlitegen.DeleteMetricSamplesBeforeParams{})
```

- [ ] **Implement the port and repository**

`internal/domain/port/metric.go`:

```go
package port

import (
	"context"
	"time"

	"winterflow/internal/domain/metrics"
)

// MetricRepository stores monitoring samples (metric_samples): tier-1 rows
// written once a minute per server and app, rolled up into coarser tiers and
// pruned per tier. Every write is insert-or-ignore on (server, app, tier,
// bucket), so replays and several API replicas are harmless.
type MetricRepository interface {
	// InsertSample writes one tier-1 row for the server (app "") and one per
	// app in apps, in one transaction, at at truncated to the minute. Apps
	// the API does not know on that server are skipped.
	InsertSample(ctx context.Context, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values) error
	// Series returns one target's rows of tier since from, oldest first
	// (appID "" is the server itself).
	Series(ctx context.Context, serverID, appID string, tier metrics.Tier, from time.Time) ([]metrics.Bucket, error)
	// AppsSeries returns every app's rows of tier since from, keyed by app.
	AppsSeries(ctx context.Context, serverID string, tier metrics.Tier, from time.Time) (map[string][]metrics.Bucket, error)
	// Prune deletes rows past their tier's retention and reports how many.
	Prune(ctx context.Context, now time.Time) (int64, error)
}
```

`internal/infra/db/repository/metric/repo.go`:

```go
// Package metric implements port.MetricRepository over metric_samples. The
// rollup math lives in the domain (metrics.Rollup) so SQLite and Postgres
// produce identical rows; SQL here only selects, inserts and deletes.
package metric

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

// Row and parameter types are SQLite's; pgsql.go converts Postgres's
// identically shaped ones.
type (
	sampleRow    = sqlitegen.MetricSample
	insertParams = sqlitegen.InsertMetricSampleParams
	listParams   = sqlitegen.ListMetricSamplesParams
	appsParams   = sqlitegen.ListServerAppMetricSamplesParams
	pruneParams  = sqlitegen.DeleteMetricSamplesBeforeParams
)

type querier interface {
	InsertMetricSample(ctx context.Context, db backend.DBTX, p insertParams) error
	ListMetricSamples(ctx context.Context, db backend.DBTX, p listParams) ([]sampleRow, error)
	ListServerAppMetricSamples(ctx context.Context, db backend.DBTX, p appsParams) ([]sampleRow, error)
	DeleteMetricSamplesBefore(ctx context.Context, db backend.DBTX, p pruneParams) (int64, error)
	ListAppIDsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]string, error)
}

// Repo is the metric_samples repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.MetricRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("metric repository: unknown database driver %q", driver)
}

// InsertSample writes the server row and the known apps' rows at tier 1.
func (r *Repo) InsertSample(ctx context.Context, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values) error {
	bucket := types.DateTime(metrics.Tier1.BucketStart(at))
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		q := r.tx.Querier(ctx)
		if err := r.insert(ctx, q, serverID, "", metrics.Tier1, bucket, 1, server); err != nil {
			return err
		}
		if len(apps) == 0 {
			return nil
		}
		known, err := r.q.ListAppIDsByServer(ctx, q, serverID)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(known))
		for _, id := range known {
			if _, ok := apps[id]; ok {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			if err := r.insert(ctx, q, serverID, id, metrics.Tier1, bucket, 1, apps[id]); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repo) insert(ctx context.Context, q backend.DBTX, serverID, appID string, tier metrics.Tier, bucket types.DateTime, samples int, v metrics.Values) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return r.q.InsertMetricSample(ctx, q, insertParams{
		ServerID: serverID, AppID: appID, Tier: int64(tier), BucketStart: bucket,
		Samples: int64(samples), Data: string(data),
	})
}

// Series returns one target's rows since from, oldest first.
func (r *Repo) Series(ctx context.Context, serverID, appID string, tier metrics.Tier, from time.Time) ([]metrics.Bucket, error) {
	rows, err := r.q.ListMetricSamples(ctx, r.tx.Querier(ctx), listParams{
		ServerID: serverID, AppID: appID, Tier: int64(tier), BucketStart: types.DateTime(from.UTC()),
	})
	if err != nil {
		return nil, err
	}
	out := make([]metrics.Bucket, 0, len(rows))
	for _, row := range rows {
		b, ok := r.hydrate(row)
		if ok {
			out = append(out, b)
		}
	}
	return out, nil
}

// AppsSeries returns every app's rows since from, keyed by app id.
func (r *Repo) AppsSeries(ctx context.Context, serverID string, tier metrics.Tier, from time.Time) (map[string][]metrics.Bucket, error) {
	rows, err := r.q.ListServerAppMetricSamples(ctx, r.tx.Querier(ctx), appsParams{
		ServerID: serverID, Tier: int64(tier), BucketStart: types.DateTime(from.UTC()),
	})
	if err != nil {
		return nil, err
	}
	out := map[string][]metrics.Bucket{}
	for _, row := range rows {
		if b, ok := r.hydrate(row); ok {
			out[row.AppID] = append(out[row.AppID], b)
		}
	}
	return out, nil
}

// Prune deletes every tier's rows older than its retention.
func (r *Repo) Prune(ctx context.Context, now time.Time) (int64, error) {
	var total int64
	for _, tier := range metrics.Tiers {
		n, err := r.q.DeleteMetricSamplesBefore(ctx, r.tx.Querier(ctx), pruneParams{
			Tier: int64(tier), BucketStart: types.DateTime(now.UTC().Add(-tier.Retention())),
		})
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// hydrate decodes a row; a row whose data is not JSON is logged and skipped
// rather than failing the whole chart.
func (r *Repo) hydrate(row sampleRow) (metrics.Bucket, bool) {
	var v metrics.Values
	if err := json.Unmarshal([]byte(row.Data), &v); err != nil {
		r.log.Warn("metric_samples: undecodable data", "server_id", row.ServerID, "app_id", row.AppID, "error", err)
		return metrics.Bucket{}, false
	}
	return metrics.Bucket{Start: row.BucketStart.Time().UTC(), Samples: int(row.Samples), Data: v}, true
}
```

`internal/infra/db/repository/metric/sqlite.go`:

```go
package metric

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) InsertMetricSample(ctx context.Context, db backend.DBTX, p insertParams) error {
	return sqlitegen.New(db).InsertMetricSample(ctx, p)
}

func (sqliteQuerier) ListMetricSamples(ctx context.Context, db backend.DBTX, p listParams) ([]sampleRow, error) {
	return sqlitegen.New(db).ListMetricSamples(ctx, p)
}

func (sqliteQuerier) ListServerAppMetricSamples(ctx context.Context, db backend.DBTX, p appsParams) ([]sampleRow, error) {
	return sqlitegen.New(db).ListServerAppMetricSamples(ctx, p)
}

func (sqliteQuerier) DeleteMetricSamplesBefore(ctx context.Context, db backend.DBTX, p pruneParams) (int64, error) {
	return sqlitegen.New(db).DeleteMetricSamplesBefore(ctx, p)
}

func (sqliteQuerier) ListAppIDsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]string, error) {
	return sqlitegen.New(db).ListAppIDsByServer(ctx, serverID)
}

```

`internal/infra/db/repository/metric/pgsql.go`:

```go
package metric

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) InsertMetricSample(ctx context.Context, db backend.DBTX, p insertParams) error {
	return pgsqlgen.New(db).InsertMetricSample(ctx, pgsqlgen.InsertMetricSampleParams(p))
}

func (pgsqlQuerier) ListMetricSamples(ctx context.Context, db backend.DBTX, p listParams) ([]sampleRow, error) {
	return convert(pgsqlgen.New(db).ListMetricSamples(ctx, pgsqlgen.ListMetricSamplesParams(p)))
}

func (pgsqlQuerier) ListServerAppMetricSamples(ctx context.Context, db backend.DBTX, p appsParams) ([]sampleRow, error) {
	return convert(pgsqlgen.New(db).ListServerAppMetricSamples(ctx, pgsqlgen.ListServerAppMetricSamplesParams(p)))
}

func (pgsqlQuerier) DeleteMetricSamplesBefore(ctx context.Context, db backend.DBTX, p pruneParams) (int64, error) {
	return pgsqlgen.New(db).DeleteMetricSamplesBefore(ctx, pgsqlgen.DeleteMetricSamplesBeforeParams(p))
}

func (pgsqlQuerier) ListAppIDsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]string, error) {
	return pgsqlgen.New(db).ListAppIDsByServer(ctx, serverID)
}

func convert(rows []pgsqlgen.MetricSample, err error) ([]sampleRow, error) {
	if err != nil {
		return nil, err
	}
	out := make([]sampleRow, len(rows))
	for i, row := range rows {
		out[i] = sampleRow(row)
	}
	return out, nil
}
```

- [ ] **Delete samples with their app (same transaction)**

Apply to `internal/infra/db/repository/app/repo.go`:

```diff
--- a/internal/infra/db/repository/app/repo.go
+++ b/internal/infra/db/repository/app/repo.go
@@ -30,6 +30,7 @@
 	UpsertApp(ctx context.Context, db backend.DBTX, p upsertParams) error
 	DeleteApp(ctx context.Context, db backend.DBTX, appID string) error
 	RenameApp(ctx context.Context, db backend.DBTX, p renameParams) error
+	DeleteMetricSamplesByApp(ctx context.Context, db backend.DBTX, appID string) error
 }
 
 // Repo is the apps repository.
@@ -84,8 +85,20 @@
 	return r.q.UpsertApp(ctx, r.tx.Querier(ctx), upsertFor(a))
 }
 
-// DeleteApp removes the app row (no error when it does not exist).
+// DeleteApp removes the app row and its monitoring samples in one
+// transaction (no error when it does not exist).
 func (r *Repo) DeleteApp(ctx context.Context, appID string) error {
+	return r.tx.WithTx(ctx, func(ctx context.Context) error {
+		return r.deleteApp(ctx, appID)
+	})
+}
+
+// deleteApp drops the app's samples, then the row; ctx must carry the
+// caller's transaction.
+func (r *Repo) deleteApp(ctx context.Context, appID string) error {
+	if err := r.q.DeleteMetricSamplesByApp(ctx, r.tx.Querier(ctx), appID); err != nil {
+		return err
+	}
 	return r.q.DeleteApp(ctx, r.tx.Querier(ctx), appID)
 }
 
@@ -115,7 +128,7 @@
 			if keep[id] {
 				continue
 			}
-			if err := r.q.DeleteApp(ctx, r.tx.Querier(ctx), id); err != nil {
+			if err := r.deleteApp(ctx, id); err != nil {
 				return err
 			}
 		}
```

Apply to `internal/infra/db/repository/app/sqlite.go`:

```diff
--- a/internal/infra/db/repository/app/sqlite.go
+++ b/internal/infra/db/repository/app/sqlite.go
@@ -34,3 +34,7 @@
 func (sqliteQuerier) RenameApp(ctx context.Context, db backend.DBTX, p renameParams) error {
 	return sqlitegen.New(db).RenameApp(ctx, p)
 }
+
+func (sqliteQuerier) DeleteMetricSamplesByApp(ctx context.Context, db backend.DBTX, appID string) error {
+	return sqlitegen.New(db).DeleteMetricSamplesByApp(ctx, appID)
+}
```

Apply to `internal/infra/db/repository/app/pgsql.go`:

```diff
--- a/internal/infra/db/repository/app/pgsql.go
+++ b/internal/infra/db/repository/app/pgsql.go
@@ -43,3 +43,7 @@
 func (pgsqlQuerier) RenameApp(ctx context.Context, db backend.DBTX, p renameParams) error {
 	return pgsqlgen.New(db).RenameApp(ctx, pgsqlgen.RenameAppParams(p))
 }
+
+func (pgsqlQuerier) DeleteMetricSamplesByApp(ctx context.Context, db backend.DBTX, appID string) error {
+	return pgsqlgen.New(db).DeleteMetricSamplesByApp(ctx, appID)
+}
```

- [ ] **Run them, expect PASS on SQLite and Postgres**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/db/...
GOTMPDIR=$HOME/.cache/wf-gotmp make test-pg TEST_PG_ARGS='-run "Metric|Sample|Insert|Series|Prune|Cascade|DeleteApp|Timestamps|Migrat"'
GOTMPDIR=$HOME/.cache/wf-gotmp make sqlc-check
```
Expected: all `ok`, and `sqlc-check` prints nothing.

- [ ] **Commit**

```bash
git add internal/infra/db internal/domain/port/metric.go
git commit -m "feat(db): store metric samples (migration 0010, metric repository)" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 8: Rollup (1→10→20→120→480) in the repository

**Files:**
- Create: `internal/infra/db/repository/metric/rollup.go`
- Modify: `internal/infra/db/sqlc/query/sqlite/metrics.sql`, `internal/infra/db/sqlc/query/pgsql/metrics.sql`, `internal/infra/db/sqlc/shapes_test.go`, `internal/domain/port/metric.go`, `internal/infra/db/repository/metric/repo.go`, `internal/infra/db/repository/metric/sqlite.go`, `internal/infra/db/repository/metric/pgsql.go`; regenerated `internal/infra/db/sqlc/gen/**`
- Test: `internal/infra/db/repository/metric/rollup_test.go`

**Interfaces:**
- Consumes: `metrics.Rollup`, `metrics.RollupPairs`, `metrics.RollupGrace`, `Tier.BucketStart`, `Tier.Retention` (Task 1); `(*Repo).insert`, `(*Repo).hydrate`, `mustInsert`, `count`, `newRepo`, `t0` (Task 7).
- Produces: `port.MetricRepository.Rollup(ctx context.Context, now time.Time) (int, error)` and query `ListMetricSamplesInRange` (params `Tier int64, FromTime, ToTime types.DateTime`).

- [ ] **Write the failing test**

`internal/infra/db/repository/metric/rollup_test.go`:

```go
package metric

import (
	"context"
	"testing"
	"time"

	"winterflow/internal/domain/metrics"
	"winterflow/internal/infra/db/dbtest"
)

// minutes inserts one tier-1 row per minute for [from, from+n).
func minutes(t *testing.T, ctx context.Context, r *Repo, from time.Time, n int, cpu func(i int) float64) {
	t.Helper()
	for i := 0; i < n; i++ {
		mustInsert(t, ctx, r, srv1, from.Add(time.Duration(i)*time.Minute), metrics.Values{"cpu": cpu(i)},
			map[string]metrics.Values{appA: {"cpu": 1}})
	}
}

func TestRollupBuildsClosedBucketsOnce(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	// 00:00-00:24: two closed 10-minute buckets and an open one.
	minutes(t, ctx, repo, t0, 25, func(i int) float64 { return float64(i) })
	now := t0.Add(25 * time.Minute)

	n, err := repo.Rollup(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	// Tier 10: buckets 00:00 and 00:10 for the server and appA (00:20 is
	// still open); tier 20: bucket 00:00-00:20 closed too, from those rows.
	if n != 6 {
		t.Fatalf("wrote %d rows, want 6", n)
	}
	got, err := repo.Series(ctx, srv1, "", metrics.Tier10, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Samples != 10 || got[0].Data["cpu"] != 4.5 || got[0].Data["cpu_max"] != 9 ||
		got[1].Data["cpu"] != 14.5 || got[1].Data["cpu_max"] != 19 {
		t.Fatalf("tier 10 = %+v", got)
	}
	// Idempotent: a second run (or a second API replica) writes nothing.
	if n, err := repo.Rollup(ctx, now); err != nil || n != 0 {
		t.Fatalf("second rollup wrote %d, %v", n, err)
	}
	if got := count(t, d, "tier = 10"); got != "4" {
		t.Fatalf("tier 10 rows = %s", got)
	}
}

func TestRollupCascadesThroughEveryTier(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	minutes(t, ctx, repo, t0, 60, func(i int) float64 {
		if i == 30 {
			return 100
		}
		return 10
	})
	// The loop runs every 10 minutes; each run only looks back over its
	// source tier's retention. At 01:02 tiers 10 and 20 are complete; at
	// 08:02 the 2-hour and 8-hour buckets starting 00:00 are closed.
	for _, now := range []time.Time{t0.Add(62 * time.Minute), t0.Add(8*time.Hour + metrics.RollupGrace)} {
		if _, err := repo.Rollup(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := repo.Series(ctx, srv1, "", metrics.Tier20, t0); len(got) != 3 || got[1].Data["cpu_max"] != 100 {
		t.Fatalf("tier 20 = %+v", got)
	}
	for _, tier := range []metrics.Tier{metrics.Tier120, metrics.Tier480} {
		got, err := repo.Series(ctx, srv1, "", tier, t0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Samples != 60 || got[0].Data["cpu_max"] != 100 {
			t.Fatalf("tier %d = %+v", tier, got)
		}
		if avg := got[0].Data["cpu"]; avg < 11.49 || avg > 11.51 { // (59*10+100)/60
			t.Fatalf("tier %d avg = %v", tier, avg)
		}
	}
}

func TestRollupSkipsDeletedApps(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	minutes(t, ctx, repo, t0, 10, func(int) float64 { return 1 })
	// The app row goes away after its samples were written but before the
	// rollup (its tier-1 rows linger until the delete path or retention).
	dbtest.Exec(t, d, `DELETE FROM apps WHERE app_id = ?`, appA)
	if _, err := repo.Rollup(ctx, t0.Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := count(t, d, "tier = 10 AND app_id = ?", appA); got != "0" {
		t.Fatalf("rolled up %s rows for a deleted app", got)
	}
	if got := count(t, d, "tier = 10 AND app_id = ''"); got != "1" {
		t.Fatalf("server rollup = %s", got)
	}
}

func TestRollupLeavesGapsAsGaps(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	// Offline from 00:10 to 00:30: no tier-1 rows, so no tier-10 rows.
	minutes(t, ctx, repo, t0, 10, func(int) float64 { return 1 })
	minutes(t, ctx, repo, t0.Add(30*time.Minute), 10, func(int) float64 { return 1 })
	if _, err := repo.Rollup(ctx, t0.Add(45*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Series(ctx, srv1, "", metrics.Tier10, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Start.Equal(t0) || !got[1].Start.Equal(t0.Add(30*time.Minute)) {
		t.Fatalf("tier 10 = %+v, want 00:00 and 00:30 only", got)
	}
}
```

- [ ] **Run it, expect FAIL**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/db/repository/metric/ -run Rollup
```
Expected: `repo.Rollup undefined`.

- [ ] **Add the range query** (insert before `-- name: DeleteMetricSamplesBefore` in both files; named args keep the generated fields readable), then `make sqlc`

SQLite and Postgres (the same text, since `sqlc.arg` works on both):

```sql
-- name: ListMetricSamplesInRange :many
SELECT * FROM metric_samples
WHERE tier = sqlc.arg(tier) AND bucket_start >= sqlc.arg(from_time) AND bucket_start < sqlc.arg(to_time)
ORDER BY server_id ASC, app_id ASC, bucket_start ASC;

```
```bash
GOTMPDIR=$HOME/.cache/wf-gotmp make sqlc
```

Add to `shapes_test.go`:

```go
	_ = sqlitegen.ListMetricSamplesInRangeParams(pgsqlgen.ListMetricSamplesInRangeParams{})
	_ = pgsqlgen.ListMetricSamplesInRangeParams(sqlitegen.ListMetricSamplesInRangeParams{})
```

- [ ] **Implement**

In `internal/domain/port/metric.go` add, after `AppsSeries`:

```go
	// Rollup builds every closed, missing bucket of each coarser tier from
	// the tier below and reports how many rows it wrote.
	Rollup(ctx context.Context, now time.Time) (int, error)
```

In `metric/repo.go` add `rangeParams  = sqlitegen.ListMetricSamplesInRangeParams` to the type aliases and `ListMetricSamplesInRange(ctx context.Context, db backend.DBTX, p rangeParams) ([]sampleRow, error)` to `querier`. Then add the two shims:

```go
// sqlite.go
func (sqliteQuerier) ListMetricSamplesInRange(ctx context.Context, db backend.DBTX, p rangeParams) ([]sampleRow, error) {
	return sqlitegen.New(db).ListMetricSamplesInRange(ctx, p)
}

// pgsql.go (above convert)
func (pgsqlQuerier) ListMetricSamplesInRange(ctx context.Context, db backend.DBTX, p rangeParams) ([]sampleRow, error) {
	return convert(pgsqlgen.New(db).ListMetricSamplesInRange(ctx, pgsqlgen.ListMetricSamplesInRangeParams(p)))
}
```

`internal/infra/db/repository/metric/rollup.go`:

```go
package metric

import (
	"context"
	"sort"
	"time"

	"winterflow/internal/domain/metrics"
	"winterflow/internal/infra/db/types"
)

// target is one rolled-up row's identity.
type target struct {
	serverID, appID string
	start           time.Time
}

// Rollup runs every rollup step, finest first, so one call can carry a
// sample all the way from tier 1 to tier 480 once the buckets are closed.
func (r *Repo) Rollup(ctx context.Context, now time.Time) (int, error) {
	total := 0
	for _, p := range metrics.RollupPairs {
		n, err := r.rollupPair(ctx, p, now)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// rollupPair writes every closed p.To bucket that has p.From rows and no
// row of its own yet. The window starts at the source tier's retention
// (older source rows are gone) and ends at the last bucket closed for
// RollupGrace. It runs in one transaction; rows of apps deleted meanwhile
// are not written.
func (r *Repo) rollupPair(ctx context.Context, p metrics.RollupPair, now time.Time) (int, error) {
	upper := p.To.BucketStart(now.Add(-metrics.RollupGrace))
	lower := p.To.BucketStart(now.Add(-p.From.Retention()))
	if !upper.After(lower) {
		return 0, nil
	}
	written := 0
	err := r.tx.WithTx(ctx, func(ctx context.Context) error {
		q := r.tx.Querier(ctx)
		window := rangeParams{FromTime: types.DateTime(lower), ToTime: types.DateTime(upper)}

		window.Tier = int64(p.To)
		done, err := r.q.ListMetricSamplesInRange(ctx, q, window)
		if err != nil {
			return err
		}
		have := make(map[target]bool, len(done))
		for _, row := range done {
			have[target{row.ServerID, row.AppID, row.BucketStart.Time().UTC()}] = true
		}

		window.Tier = int64(p.From)
		src, err := r.q.ListMetricSamplesInRange(ctx, q, window)
		if err != nil {
			return err
		}
		groups := map[target][]metrics.Bucket{}
		for _, row := range src {
			k := target{row.ServerID, row.AppID, p.To.BucketStart(row.BucketStart.Time())}
			if have[k] {
				continue
			}
			if b, ok := r.hydrate(row); ok {
				groups[k] = append(groups[k], b)
			}
		}

		keys := make([]target, 0, len(groups))
		for k := range groups {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := keys[i], keys[j]
			if a.serverID != b.serverID {
				return a.serverID < b.serverID
			}
			if a.appID != b.appID {
				return a.appID < b.appID
			}
			return a.start.Before(b.start)
		})

		apps := map[string]map[string]bool{} // server -> existing app ids
		for _, k := range keys {
			if k.appID != "" {
				known, ok := apps[k.serverID]
				if !ok {
					ids, err := r.q.ListAppIDsByServer(ctx, q, k.serverID)
					if err != nil {
						return err
					}
					known = make(map[string]bool, len(ids))
					for _, id := range ids {
						known[id] = true
					}
					apps[k.serverID] = known
				}
				if !known[k.appID] {
					continue
				}
			}
			b := metrics.Rollup(k.start, groups[k])
			if err := r.insert(ctx, q, k.serverID, k.appID, p.To, types.DateTime(b.Start), b.Samples, b.Data); err != nil {
				return err
			}
			written++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return written, nil
}
```

- [ ] **Run it, expect PASS on both engines**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/db/repository/metric/
GOTMPDIR=$HOME/.cache/wf-gotmp make test-pg TEST_PG_ARGS='-run Rollup'
GOTMPDIR=$HOME/.cache/wf-gotmp make sqlc-check
```
Expected: `ok` on SQLite and Postgres; `sqlc-check` clean.

- [ ] **Commit**

```bash
git add internal/infra/db internal/domain/port/metric.go
git commit -m "feat(db): roll metric samples up through the coarser tiers" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 9: API: store metrics events, maintenance loop, Deps

**Files:**
- Create: `internal/infra/bootstrap/metrics.go`
- Modify: `internal/infra/bootstrap/events.go`, `internal/infra/bootstrap/core.go`, `internal/infra/bootstrap/deps.go`, `CLAUDE.md` (one Architecture paragraph)
- Test: `internal/infra/bootstrap/metrics_test.go`

**Interfaces:**
- Consumes: `metrics.Sample`, `metrics.SampleTime`, `metrics.Latest`, `metrics.LatestStore`, `metrics.Values` (Task 1); `metric.New`, `(*metric.Repo).InsertSample/Rollup/Prune` (Tasks 7–8); `cfg.IsMetricsEnabled` (Task 2); existing `newTestSink`, `recorder` (events_test.go), `dbtest.SeedServer`.
- Produces: `bootstrap.Deps.MetricRepository port.MetricRepository` and `bootstrap.Deps.LatestMetrics *metrics.LatestStore` (consumed by Task 10); `eventSink.metrics metricsStore`, `eventSink.latest *metrics.LatestStore`, and `(*eventSink).storeMetrics`; `startEventsSubscriber(..., indexSync serverSync, ms metricsStore, latest *metrics.LatestStore)`; `runMetricsMaintenance(ctx, metricsMaintainer, *metrics.LatestStore, time.Duration, *logger.Logger)` with `metricsMaintenanceInterval = 10 * time.Minute`, `metricsPruneEvery = time.Hour`, `metricsMaintenanceTimeout = 2 * time.Minute`, `latestTTL = time.Hour`.

- [ ] **Write the failing test**

`internal/infra/bootstrap/metrics_test.go`:

```go
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
	mu    sync.Mutex
	calls []insertCall
	err   error
}

func (f *fakeMetricsStore) InsertSample(_ context.Context, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values) error {
	f.mu.Lock()
	defer f.mu.Unlock()
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
}

func (f *fakeMaintainer) Rollup(context.Context, time.Time) (int, error) {
	f.rollups++
	if f.fail {
		return 0, errors.New("locked")
	}
	return 1, nil
}

func (f *fakeMaintainer) Prune(context.Context, time.Time) (int64, error) {
	f.prunes++
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
```

- [ ] **Run it, expect FAIL**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/infra/bootstrap/ -run "Metrics|WireCoreStores"
```
Expected: `sink.metrics undefined`, `undefined: metricsMaintenance`.

- [ ] **Implement**

`internal/infra/bootstrap/metrics.go`:

```go
package bootstrap

import (
	"context"
	"time"

	"winterflow/internal/domain/metrics"
	"winterflow/pkg/logger"
)

const (
	// metricsMaintenanceInterval is how often closed buckets are rolled up.
	metricsMaintenanceInterval = 10 * time.Minute
	// metricsPruneEvery is how often rows past retention are deleted.
	metricsPruneEvery = time.Hour
	// metricsMaintenanceTimeout bounds one tick's DB work.
	metricsMaintenanceTimeout = 2 * time.Minute
	// latestTTL drops a server's latest sample once it stopped reporting
	// for this long (there is no server-delete path to hook instead).
	latestTTL = time.Hour
)

// metricsMaintainer is the slice of the metric repository the loop needs.
type metricsMaintainer interface {
	Rollup(ctx context.Context, now time.Time) (int, error)
	Prune(ctx context.Context, now time.Time) (int64, error)
}

// metricsMaintenance rolls up closed buckets on every tick, prunes past
// retention at most once per metricsPruneEvery, and drops stale latest
// samples. Rollup runs first so a bucket is never pruned before it was
// rolled up.
type metricsMaintenance struct {
	repo      metricsMaintainer
	latest    *metrics.LatestStore
	log       *logger.Logger
	lastPrune time.Time
}

func (m *metricsMaintenance) tick(ctx context.Context, now time.Time) {
	ctx, cancel := context.WithTimeout(ctx, metricsMaintenanceTimeout)
	defer cancel()
	if n, err := m.repo.Rollup(ctx, now); err != nil {
		m.log.Warn("metrics rollup failed", "error", err)
	} else if n > 0 {
		m.log.Debug("metrics rolled up", "rows", n)
	}
	if now.Sub(m.lastPrune) >= metricsPruneEvery {
		if n, err := m.repo.Prune(ctx, now); err != nil {
			m.log.Warn("metrics prune failed", "error", err)
		} else {
			m.lastPrune = now
			m.log.Debug("metrics pruned", "rows", n)
		}
	}
	m.latest.Prune(now.Add(-latestTTL))
}

// runMetricsMaintenance ticks immediately (catching up after downtime) and
// then every interval until ctx is done.
func runMetricsMaintenance(ctx context.Context, repo metricsMaintainer, latest *metrics.LatestStore, interval time.Duration, log *logger.Logger) {
	m := &metricsMaintenance{repo: repo, latest: latest, log: log}
	m.tick(ctx, time.Now())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.tick(ctx, now)
		}
	}
}
```

Apply to `internal/infra/bootstrap/events.go`:

```diff
--- a/internal/infra/bootstrap/events.go
+++ b/internal/infra/bootstrap/events.go
@@ -6,6 +6,7 @@
 	"time"
 
 	"winterflow/internal/domain/command"
+	"winterflow/internal/domain/metrics"
 	"winterflow/internal/domain/model"
 	"winterflow/internal/domain/port"
 	"winterflow/internal/domain/service/status"
@@ -35,6 +36,11 @@
 	SaveCapabilities(ctx context.Context, serverID string, capabilities map[string]string, features map[string]bool) error
 }
 
+// metricsStore is the slice of the metric repository the event sink needs.
+type metricsStore interface {
+	InsertSample(ctx context.Context, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values) error
+}
+
 // statusFanout pushes an unsolicited status notification to every member of
 // the org that owns the server. Function fields keep it trivially fakeable.
 type statusFanout struct {
@@ -81,6 +87,11 @@
 	store eventStore
 	fan   *statusFanout
 	log   *logger.Logger
+	// metrics stores server.metrics samples; nil when METRICS_ENABLED=false
+	// (the events are then dropped). latest keeps each server's newest host
+	// sample for the dashboard.
+	metrics metricsStore
+	latest  *metrics.LatestStore
 	// sync, when set, fills the API's per-server indexes from the agent: it
 	// starts when a server comes online (an unknown->online transition, or a
 	// capabilities event: the agent (re)registered) and is nudged by every
@@ -135,6 +146,9 @@
 		if transition {
 			s.fan.serverStatus(ctx, ev.ServerID, status.LivenessOnline)
 		}
+	case bus.EventServerMetrics:
+		s.storeMetrics(ctx, ev, now)
+		return
 	default:
 		s.log.Warn("unknown event kind", "kind", ev.Kind)
 		return
@@ -149,6 +163,33 @@
 	}
 }
 
+// storeMetrics writes one server.metrics sample (tier 1) and remembers the
+// host part as the server's latest. A write failure is logged; the next
+// minute's sample is independent. Metrics never touch liveness or SSE.
+func (s *eventSink) storeMetrics(ctx context.Context, ev bus.EventMessage, now time.Time) {
+	if s.metrics == nil {
+		return
+	}
+	var sample metrics.Sample
+	if err := json.Unmarshal(ev.Payload, &sample); err != nil {
+		s.log.Warn("failed to decode metrics event", "server_id", ev.ServerID, "error", err)
+		return
+	}
+	at := metrics.SampleTime(sample, now)
+	apps := make(map[string]metrics.Values, len(sample.Apps))
+	for _, a := range sample.Apps {
+		if a.AppID != "" {
+			apps[a.AppID] = a.Values()
+		}
+	}
+	if err := s.metrics.InsertSample(ctx, ev.ServerID, at, sample.Server.Values(), apps); err != nil {
+		s.log.Warn("store metrics failed", "server_id", ev.ServerID, "error", err)
+	}
+	if s.latest != nil {
+		s.latest.Set(ev.ServerID, metrics.Latest{At: at, Server: sample.Server})
+	}
+}
+
 // sweep flips servers whose liveness expired to unknown and pushes the
 // transition. Factored out of the ticker loop for testability.
 func (s *eventSink) sweep(ctx context.Context, cache *status.Cache, now time.Time) {
@@ -161,13 +202,15 @@
 // liveness, capabilities, status), updating the in-memory status cache and the
 // DB, and pushing transitions over SSE. It also runs the expiry sweeper. It is
 // the API's consumer of events:<region>.
-func startEventsSubscriber(ctx context.Context, b bus.Bus, cache *status.Cache, serverRepo port.ServerRepository, nm port.NotificationManager, cfg *config.ServerConfig, log *logger.Logger, indexSync serverSync) {
+func startEventsSubscriber(ctx context.Context, b bus.Bus, cache *status.Cache, serverRepo port.ServerRepository, nm port.NotificationManager, cfg *config.ServerConfig, log *logger.Logger, indexSync serverSync, ms metricsStore, latest *metrics.LatestStore) {
 	sink := &eventSink{
-		cache: cache,
-		store: serverRepo,
-		fan:   newStatusFanout(serverRepo, nm, log),
-		log:   log,
-		sync:  indexSync,
+		cache:   cache,
+		store:   serverRepo,
+		fan:     newStatusFanout(serverRepo, nm, log),
+		log:     log,
+		sync:    indexSync,
+		metrics: ms,
+		latest:  latest,
 	}
 	bus.SubscribeJSON(ctx, b, cfg.GetBusEventsQueue(), log, func(ev bus.EventMessage) {
 		sink.handle(ctx, ev)
```

Apply to `internal/infra/bootstrap/core.go`:

```diff
--- a/internal/infra/bootstrap/core.go
+++ b/internal/infra/bootstrap/core.go
@@ -3,11 +3,13 @@
 import (
 	"context"
 
+	"winterflow/internal/domain/metrics"
 	notificationsvc "winterflow/internal/domain/service/notification"
 	"winterflow/internal/domain/service/status"
 	"winterflow/internal/infra/db"
 	apprepo "winterflow/internal/infra/db/repository/app"
 	appdomainrepo "winterflow/internal/infra/db/repository/appdomain"
+	metricrepo "winterflow/internal/infra/db/repository/metric"
 	serverrepo "winterflow/internal/infra/db/repository/server"
 	serverdomainrepo "winterflow/internal/infra/db/repository/serverdomain"
 	userrepo "winterflow/internal/infra/db/repository/user"
@@ -35,8 +37,18 @@
 	dispatcher := dispatch.NewManager(b, nm, cfg, log)
 	statusCache := status.NewCache(statusTTL)
 
+	// Metrics: the events sink stores server.metrics samples and the
+	// maintenance loop rolls them up and prunes them. Both are off when
+	// METRICS_ENABLED=false; the chart routes then serve empty series.
+	latest := metrics.NewLatestStore()
+	var ms metricsStore
+	if cfg.IsMetricsEnabled() {
+		ms = r.metric
+		go runMetricsMaintenance(ctx, r.metric, latest, metricsMaintenanceInterval, log)
+	}
+
 	startResponseSubscriber(ctx, b, dispatcher, cfg, log)
-	startEventsSubscriber(ctx, b, statusCache, r.server, nm, cfg, log, newDomainSync(ctx, dispatcher, r.serverDomain, log))
+	startEventsSubscriber(ctx, b, statusCache, r.server, nm, cfg, log, newDomainSync(ctx, dispatcher, r.serverDomain, log), ms, latest)
 
 	return &Deps{
 		Log:                    log,
@@ -49,16 +61,19 @@
 		CommandDispatcher:      dispatcher,
 		NotificationManager:    nm,
 		StatusCache:            statusCache,
+		MetricRepository:       r.metric,
+		LatestMetrics:          latest,
 	}, r.server
 }
 
-// repos are the five DB repositories, built for the connection's driver.
+// repos are the six DB repositories, built for the connection's driver.
 type repos struct {
 	user         *userrepo.Repo
 	server       *serverrepo.Repo
 	app          *apprepo.Repo
 	appDomain    *appdomainrepo.Repo
 	serverDomain *serverdomainrepo.Repo
+	metric       *metricrepo.Repo
 }
 
 func newRepos(conn *db.DB, log *logger.Logger) (repos, error) {
@@ -78,6 +93,9 @@
 	if r.appDomain, err = appdomainrepo.New(conn.Tx, conn.Driver, log); err != nil {
 		return r, err
 	}
-	r.serverDomain, err = serverdomainrepo.New(conn.Tx, conn.Driver, log)
+	if r.serverDomain, err = serverdomainrepo.New(conn.Tx, conn.Driver, log); err != nil {
+		return r, err
+	}
+	r.metric, err = metricrepo.New(conn.Tx, conn.Driver, log)
 	return r, err
 }
```

Apply to `internal/infra/bootstrap/deps.go`:

```diff
--- a/internal/infra/bootstrap/deps.go
+++ b/internal/infra/bootstrap/deps.go
@@ -1,6 +1,7 @@
 package bootstrap
 
 import (
+	"winterflow/internal/domain/metrics"
 	"winterflow/internal/domain/port"
 	"winterflow/internal/domain/service/status"
 	"winterflow/pkg/config"
@@ -27,4 +28,8 @@
 	CommandDispatcher      port.CommandDispatcher
 	NotificationManager    port.NotificationManager
 	StatusCache            *status.Cache
+	// MetricRepository serves the monitoring charts; LatestMetrics holds
+	// each server's newest host sample for the dashboard.
+	MetricRepository port.MetricRepository
+	LatestMetrics    *metrics.LatestStore
 }
```

Append this paragraph to the Architecture section of `CLAUDE.md`, after the **Orchestration** paragraph:

```markdown

**Monitoring (metrics):** the agent/standalone runs `appagent.StartMetrics`: a `server.metrics` event every 60 s built from procfs/sysfs/cgroupfs reads (`internal/infra/sysmetrics`, no forks; containers come from the status reporter's `StatusSnapshot`, never a second `docker ps`) plus an hourly `ScanDisk` (`docker system df -v`, 5-min deadline). The model, tiers and rollup math live in `internal/domain/metrics`. The API's events sink writes tier-1 rows into `metric_samples` (insert-or-ignore; app rows only for apps the API knows on that server) and keeps the latest host sample in memory (`LatestStore`); `runMetricsMaintenance` (wired in `WireCore`) rolls up 1→10→20→120→480 every 10 min and prunes per tier hourly. Charts are pulled (`/api/v1/metrics/*`), never pushed over SSE. `METRICS_ENABLED=false` switches the whole pipeline off.
```

- [ ] **Run it, expect PASS**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test -race ./internal/infra/bootstrap/
```
Expected: `ok  winterflow/internal/infra/bootstrap`.

- [ ] **Commit**

```bash
git add internal/infra/bootstrap CLAUDE.md
git commit -m "feat(api): store server.metrics and run rollup/retention" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 10: Chart API: /api/v1/metrics/{server,app,overview}

**Files:**
- Create: `internal/app/web/handler/monitoring/handler.go`, `internal/app/web/handler/monitoring/routes.go`, `internal/app/web/handler/monitoring/series.go`, `internal/app/web/handler/monitoring/overview.go`
- Modify: `internal/app/web/routes.go`, `internal/app/web/contract/matrix_test.go` (probes), `api/openapi.yaml` (generated), `internal/app/web/testdata/routes.golden` (generated)
- Test: `internal/app/web/handler/monitoring/handler_test.go`, `internal/app/web/contract/metrics_test.go`

**Interfaces:**
- Consumes: `bootstrap.Deps.MetricRepository`, `bootstrap.Deps.LatestMetrics` (Task 9); `port.ServerRepository.UserOwnsServer/GetServers`; `status.Cache.ServerLiveness/AppStatuses`; `util.RequireUser`, `util.RequireServerAccess`, `util.Success`, `util.Error`; `metrics.ParseRange`, `metrics.ToPoint`, `Point.Pick` (Task 1).
- Produces (package `winterflow/internal/app/web/handler/monitoring`; the name avoids clashing with the domain `metrics` package): `NewHandler(*Deps) *Handler`, `Deps{Logger, Servers, Metrics, Latest, StatusCache}`, `Routes(api.Guards) []api.Route`; query types `SeriesQuery{server_id, range}` and `AppSeriesQuery{server_id, app_id, range}` (range enum `1h,12h,24h,7d,30d`); responses `SeriesResponse{interval_seconds int, points []metrics.Point}`, `ServerSeriesResponse{interval_seconds, points, apps []AppSeries}`, `AppSeries{app_id, points}` (points trimmed to `cpu`, `mem_used` and their peaks), `OverviewResponse{servers []ServerOverview}`, `ServerOverview{server_id, liveness, latest *metrics.Latest (null before the first sample), sparkline []metrics.Point (t, cpu, mem_used, mem_total, disk_pct + peaks, last hour of tier 1), apps AppCounts}`, `AppCounts{running, stopped, problematic}`. Each point has a `"t"` key (unix seconds); every field has `<field>_max`.
- Status order on series routes: no `server_id` → 400, foreign server → 403, bad/missing range → 400.

- [ ] **Write the failing unit test**

`internal/app/web/handler/monitoring/handler_test.go`:

```go
package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	"winterflow/pkg/logger"
)

// fakeServers owns srv-1 for u1; every other port method panics if called.
type fakeServers struct {
	port.ServerRepository
	err error
}

func (f fakeServers) UserOwnsServer(_ context.Context, userID, serverID string) (bool, error) {
	return userID == "u1" && serverID == "srv-1", f.err
}

func (f fakeServers) GetServers(_ context.Context, userID string) ([]model.Server, error) {
	if f.err != nil {
		return nil, f.err
	}
	if userID != "u1" {
		return nil, nil
	}
	return []model.Server{{ID: "srv-1"}}, nil
}

type seriesCall struct {
	serverID, appID string
	tier            metrics.Tier
	since           time.Time
}

type fakeRepo struct {
	port.MetricRepository
	calls []seriesCall
	rows  []metrics.Bucket
	apps  map[string][]metrics.Bucket
	err   error
}

func (f *fakeRepo) Series(_ context.Context, serverID, appID string, tier metrics.Tier, since time.Time) ([]metrics.Bucket, error) {
	f.calls = append(f.calls, seriesCall{serverID, appID, tier, since})
	return f.rows, f.err
}

func (f *fakeRepo) AppsSeries(context.Context, string, metrics.Tier, time.Time) (map[string][]metrics.Bucket, error) {
	return f.apps, f.err
}

var now = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func newHandler(repo *fakeRepo, servers fakeServers) (*Handler, *status.Cache, *metrics.LatestStore) {
	cache := status.NewCache(time.Minute)
	latest := metrics.NewLatestStore()
	h := NewHandler(&Deps{
		Logger:      logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
		Servers:     servers,
		Metrics:     repo,
		Latest:      latest,
		StatusCache: cache,
	})
	h.now = func() time.Time { return now }
	return h, cache, latest
}

func get(h http.HandlerFunc, url, userID string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, url, nil)
	if userID != "" {
		r = token.SetUserInfo(r, token.User{ID: "p_" + userID, Attributes: map[string]any{"user_id": userID}})
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var body struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return body.Data
}

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{})
	want := map[string]bool{ // "METHOD path" -> documents 403
		"GET /api/v1/metrics/server":   true,
		"GET /api/v1/metrics/app":      true,
		"GET /api/v1/metrics/overview": false,
	}
	got := map[string]bool{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil || rt.Auth != api.User || rt.Accepted || len(rt.Middleware) != 0 {
			t.Errorf("%s: want a synchronous User route without middleware", key)
		}
		got[key] = slices.Contains(rt.Errors, http.StatusForbidden)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v, want %v", got, want)
	}
}

func TestServerSeriesMapsRangeToTier(t *testing.T) {
	cases := map[string]struct {
		tier     metrics.Tier
		span     time.Duration
		interval int
	}{
		"1h":  {metrics.Tier1, time.Hour, 60},
		"12h": {metrics.Tier10, 12 * time.Hour, 600},
		"24h": {metrics.Tier20, 24 * time.Hour, 1200},
		"7d":  {metrics.Tier120, 7 * 24 * time.Hour, 7200},
		"30d": {metrics.Tier480, 30 * 24 * time.Hour, 28800},
	}
	for rng, want := range cases {
		repo := &fakeRepo{rows: []metrics.Bucket{{Start: now.Add(-time.Hour), Samples: 1, Data: metrics.Values{"cpu": 5}}},
			apps: map[string][]metrics.Bucket{
				"b": {{Start: now, Samples: 1, Data: metrics.Values{"cpu": 2, "mem_used": 3, "disk": 9}}},
				"a": {{Start: now, Samples: 1, Data: metrics.Values{"cpu": 1}}},
			}}
		h, _, _ := newHandler(repo, fakeServers{})
		w := get(h.ServerSeries, "/api/v1/metrics/server?server_id=srv-1&range="+rng, "u1")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", rng, w.Code, w.Body.String())
		}
		if c := repo.calls[0]; c.tier != want.tier || !c.since.Equal(now.Add(-want.span)) || c.appID != "" {
			t.Errorf("%s: series call = %+v", rng, c)
		}
		body := decode[ServerSeriesResponse](t, w)
		if body.IntervalSeconds != want.interval || len(body.Points) != 1 || body.Points[0]["cpu_max"] != 5 {
			t.Errorf("%s: body = %+v", rng, body)
		}
		// Apps sorted by id, trimmed to the stacked fields.
		if len(body.Apps) != 2 || body.Apps[0].AppID != "a" || len(body.Apps[1].Points[0]) != 5 {
			t.Errorf("%s: apps = %+v", rng, body.Apps)
		}
	}
}

func TestSeriesRejectsBadInput(t *testing.T) {
	h, _, _ := newHandler(&fakeRepo{}, fakeServers{})
	cases := []struct {
		name string
		fn   http.HandlerFunc
		url  string
		user string
		code int
	}{
		{"anonymous", h.ServerSeries, "/x?server_id=srv-1&range=1h", "", http.StatusUnauthorized},
		{"no server", h.ServerSeries, "/x?range=1h", "u1", http.StatusBadRequest},
		{"foreign server", h.ServerSeries, "/x?server_id=srv-2&range=1h", "u1", http.StatusForbidden},
		{"other user", h.AppSeries, "/x?server_id=srv-1&app_id=a&range=1h", "u2", http.StatusForbidden},
		{"bad range", h.ServerSeries, "/x?server_id=srv-1&range=2h", "u1", http.StatusBadRequest},
		{"no range", h.AppSeries, "/x?server_id=srv-1&app_id=a", "u1", http.StatusBadRequest},
		{"no app", h.AppSeries, "/x?server_id=srv-1&range=1h", "u1", http.StatusBadRequest},
	}
	for _, tc := range cases {
		if w := get(tc.fn, tc.url, tc.user); w.Code != tc.code {
			t.Errorf("%s: %d, want %d (%s)", tc.name, w.Code, tc.code, w.Body.String())
		}
	}
	// The ownership check fails closed on a lookup error.
	h2, _, _ := newHandler(&fakeRepo{}, fakeServers{err: errors.New("db down")})
	if w := get(h2.ServerSeries, "/x?server_id=srv-1&range=1h", "u1"); w.Code != http.StatusForbidden {
		t.Errorf("lookup error: %d", w.Code)
	}
}

func TestAppSeries(t *testing.T) {
	repo := &fakeRepo{rows: []metrics.Bucket{}}
	h, _, _ := newHandler(repo, fakeServers{})
	w := get(h.AppSeries, "/x?server_id=srv-1&app_id=app-9&range=7d", "u1")
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if c := repo.calls[0]; c.serverID != "srv-1" || c.appID != "app-9" || c.tier != metrics.Tier120 {
		t.Fatalf("call = %+v", c)
	}
	body := decode[SeriesResponse](t, w)
	if body.Points == nil || len(body.Points) != 0 || body.IntervalSeconds != 7200 {
		t.Fatalf("empty series = %+v (points must be [], not null)", body)
	}
}

func TestSeriesRepositoryError(t *testing.T) {
	h, _, _ := newHandler(&fakeRepo{err: errors.New("boom")}, fakeServers{})
	if w := get(h.ServerSeries, "/x?server_id=srv-1&range=1h", "u1"); w.Code != http.StatusBadRequest {
		t.Fatalf("server: %d", w.Code)
	}
	if w := get(h.AppSeries, "/x?server_id=srv-1&app_id=a&range=1h", "u1"); w.Code != http.StatusBadRequest {
		t.Fatalf("app: %d", w.Code)
	}
}

func TestOverview(t *testing.T) {
	repo := &fakeRepo{rows: []metrics.Bucket{{Start: now.Add(-time.Minute), Samples: 1,
		Data: metrics.Values{"cpu": 10, "mem_used": 1, "mem_total": 2, "disk_pct": 50, "load1": 3}}}}
	h, cache, latest := newHandler(repo, fakeServers{})
	cache.SetAppStatus("srv-1", []command.AppStatus{
		{AppID: "a", StatusCode: command.ContainerStatusActive},
		{AppID: "b", StatusCode: command.ContainerStatusRestarting},
		{AppID: "c", StatusCode: command.ContainerStatusStopped},
		{AppID: "d", StatusCode: command.ContainerStatusProblematic},
		{AppID: "e", StatusCode: command.ContainerStatusUnknown},
	}, now)
	cpu := 10.0
	latest.Set("srv-1", metrics.Latest{At: now, Server: metrics.ServerSample{CPU: &cpu}})

	w := get(h.Overview, "/api/v1/metrics/overview", "u1")
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	body := decode[OverviewResponse](t, w)
	if len(body.Servers) != 1 {
		t.Fatalf("servers = %+v", body.Servers)
	}
	s := body.Servers[0]
	if s.Liveness != status.LivenessOnline || s.Apps != (AppCounts{Running: 2, Stopped: 1, Problematic: 1}) {
		t.Fatalf("overview = %+v", s)
	}
	if s.Latest == nil || *s.Latest.Server.CPU != 10 {
		t.Fatalf("latest = %+v", s.Latest)
	}
	if len(s.Sparkline) != 1 || s.Sparkline[0]["disk_pct"] != 50 {
		t.Fatalf("sparkline = %+v", s.Sparkline)
	}
	if _, ok := s.Sparkline[0]["load1"]; ok {
		t.Fatal("sparkline carries fields it does not chart")
	}
	if c := repo.calls[0]; c.tier != metrics.Tier1 || !c.since.Equal(now.Add(-time.Hour)) {
		t.Fatalf("sparkline query = %+v", c)
	}

	// No sample yet: latest is null, sparkline [].
	h2, _, _ := newHandler(&fakeRepo{err: errors.New("boom")}, fakeServers{})
	s = decode[OverviewResponse](t, get(h2.Overview, "/x", "u1")).Servers[0]
	if s.Latest != nil || s.Sparkline == nil || len(s.Sparkline) != 0 {
		t.Fatalf("empty overview = %+v", s)
	}
	// Another org's user sees nothing; a lookup error is a 400.
	if got := decode[OverviewResponse](t, get(h.Overview, "/x", "u2")); len(got.Servers) != 0 {
		t.Fatalf("u2 sees %+v", got)
	}
	h3, _, _ := newHandler(&fakeRepo{}, fakeServers{err: errors.New("db down")})
	if w := get(h3.Overview, "/x", "u1"); w.Code != http.StatusBadRequest {
		t.Fatalf("lookup error: %d", w.Code)
	}
	if w := get(h.Overview, "/x", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", w.Code)
	}
}
```

- [ ] **Run it, expect FAIL**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/app/web/handler/monitoring/
```
Expected: `no non-test Go files` / `undefined: NewHandler`.

- [ ] **Implement the handler**

`internal/app/web/handler/monitoring/handler.go`:

```go
// Package monitoring serves the metrics charts: per-server and per-app time
// series from metric_samples and the dashboard overview (latest sample,
// 1-hour sparklines, app counts) for the caller's servers.
package monitoring

import (
	"time"

	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	"winterflow/pkg/logger"
)

// Handler serves /api/v1/metrics/*.
type Handler struct {
	servers port.ServerRepository
	repo    port.MetricRepository
	latest  *metrics.LatestStore
	status  *status.Cache
	log     *logger.Logger
	now     func() time.Time
}

// Deps are the handler's collaborators (all from bootstrap.Deps).
type Deps struct {
	Logger      *logger.Logger
	Servers     port.ServerRepository
	Metrics     port.MetricRepository
	Latest      *metrics.LatestStore
	StatusCache *status.Cache
}

// NewHandler builds the handler.
func NewHandler(d *Deps) *Handler {
	return &Handler{
		servers: d.Servers,
		repo:    d.Metrics,
		latest:  d.Latest,
		status:  d.StatusCache,
		log:     d.Logger,
		now:     time.Now,
	}
}
```

`internal/app/web/handler/monitoring/routes.go`:

```go
package monitoring

import (
	"net/http"

	"winterflow/internal/app/web/api"
)

// SeriesQuery addresses one server's chart.
type SeriesQuery struct {
	ServerID string `query:"server_id" required:"true" description:"Target server id."`
	Range    string `query:"range" required:"true" enum:"1h,12h,24h,7d,30d" description:"Chart range: 1h (1-minute points), 12h (10 min), 24h (20 min), 7d (2 h), 30d (8 h)."`
}

// AppSeriesQuery addresses one app's chart.
type AppSeriesQuery struct {
	ServerID string `query:"server_id" required:"true" description:"Server the app runs on."`
	AppID    string `query:"app_id" required:"true" description:"Target app id."`
	Range    string `query:"range" required:"true" enum:"1h,12h,24h,7d,30d" description:"Chart range (see /api/v1/metrics/server)."`
}

// Routes is the monitoring slice of the route table. Server-addressed
// routes check org ownership in the handler (403); the overview lists only
// the caller's servers.
func (h *Handler) Routes(api.Guards) []api.Route {
	server := []int{http.StatusBadRequest, http.StatusForbidden}
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/metrics/server", Handler: h.ServerSeries, Auth: api.User,
			Summary: "A server's resource charts", Tag: "metrics",
			Description: "Average and peak (`<field>_max`) per point; `interval_seconds` is the point spacing, so a " +
				"larger gap between two points means the server did not report. `apps` holds each app's CPU and memory " +
				"for the stacked charts.",
			Query: SeriesQuery{}, Response: ServerSeriesResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/metrics/app", Handler: h.AppSeries, Auth: api.User,
			Summary: "An app's resource charts", Tag: "metrics",
			Query: AppSeriesQuery{}, Response: SeriesResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/metrics/overview", Handler: h.Overview, Auth: api.User,
			Summary: "Dashboard health of the caller's servers", Tag: "metrics",
			Response: OverviewResponse{}, Errors: []int{http.StatusBadRequest},
		},
	}
}
```

`internal/app/web/handler/monitoring/series.go`:

```go
package monitoring

import (
	"net/http"
	"sort"
	"time"

	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/metrics"
)

// SeriesResponse is one chart series: points oldest first, spaced
// interval_seconds apart where the target reported.
type SeriesResponse struct {
	IntervalSeconds int             `json:"interval_seconds"`
	Points          []metrics.Point `json:"points"`
}

// AppSeries is one app's CPU and memory points for the stacked charts.
type AppSeries struct {
	AppID  string          `json:"app_id"`
	Points []metrics.Point `json:"points"`
}

// ServerSeriesResponse is a server's series plus its apps' CPU/memory.
type ServerSeriesResponse struct {
	IntervalSeconds int             `json:"interval_seconds"`
	Points          []metrics.Point `json:"points"`
	Apps            []AppSeries     `json:"apps"`
}

// stackedFields are the app fields the server page stacks per app.
var stackedFields = []string{"cpu", "mem_used"}

// toPoints renders stored rows as chart points.
func toPoints(rows []metrics.Bucket) []metrics.Point {
	out := make([]metrics.Point, 0, len(rows))
	for _, b := range rows {
		out = append(out, metrics.ToPoint(b))
	}
	return out
}

// target validates the shared query: server_id present and owned by the
// caller, range known. It writes the error itself; ok reports success.
func (h *Handler) target(w http.ResponseWriter, r *http.Request) (serverID string, tier metrics.Tier, since time.Time, ok bool) {
	userID, ok := util.RequireUser(w, r)
	if !ok {
		return "", 0, time.Time{}, false
	}
	q := r.URL.Query()
	serverID = q.Get("server_id")
	if serverID == "" {
		util.Error(w, "server_id is required", nil)
		return "", 0, time.Time{}, false
	}
	if !util.RequireServerAccess(w, r, h.servers, userID, serverID) {
		return "", 0, time.Time{}, false
	}
	tier, span, known := metrics.ParseRange(q.Get("range"))
	if !known {
		util.Error(w, "range must be one of 1h, 12h, 24h, 7d, 30d", nil)
		return "", 0, time.Time{}, false
	}
	return serverID, tier, h.now().Add(-span), true
}

// ServerSeries serves GET /api/v1/metrics/server.
func (h *Handler) ServerSeries(w http.ResponseWriter, r *http.Request) {
	serverID, tier, since, ok := h.target(w, r)
	if !ok {
		return
	}
	rows, err := h.repo.Series(r.Context(), serverID, "", tier, since)
	if err != nil {
		h.log.Error("metrics: server series", "error", err, "server_id", serverID)
		util.Error(w, "failed to load metrics", nil)
		return
	}
	byApp, err := h.repo.AppsSeries(r.Context(), serverID, tier, since)
	if err != nil {
		h.log.Error("metrics: apps series", "error", err, "server_id", serverID)
		util.Error(w, "failed to load metrics", nil)
		return
	}
	ids := make([]string, 0, len(byApp))
	for id := range byApp {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	apps := make([]AppSeries, 0, len(ids))
	for _, id := range ids {
		pts := toPoints(byApp[id])
		for i, p := range pts {
			pts[i] = p.Pick(stackedFields...)
		}
		apps = append(apps, AppSeries{AppID: id, Points: pts})
	}
	util.Success(w, "ok", ServerSeriesResponse{
		IntervalSeconds: int(tier.Duration().Seconds()),
		Points:          toPoints(rows),
		Apps:            apps,
	})
}

// AppSeries serves GET /api/v1/metrics/app. Rows are keyed by server and
// app, so an app id from another server simply has no points.
func (h *Handler) AppSeries(w http.ResponseWriter, r *http.Request) {
	serverID, tier, since, ok := h.target(w, r)
	if !ok {
		return
	}
	appID := r.URL.Query().Get("app_id")
	if appID == "" {
		util.Error(w, "app_id is required", nil)
		return
	}
	rows, err := h.repo.Series(r.Context(), serverID, appID, tier, since)
	if err != nil {
		h.log.Error("metrics: app series", "error", err, "server_id", serverID, "app_id", appID)
		util.Error(w, "failed to load metrics", nil)
		return
	}
	util.Success(w, "ok", SeriesResponse{IntervalSeconds: int(tier.Duration().Seconds()), Points: toPoints(rows)})
}
```

`internal/app/web/handler/monitoring/overview.go`:

```go
package monitoring

import (
	"net/http"
	"time"

	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/service/status"
)

// sparklineFields are the host fields of the dashboard's 1-hour sparklines.
var sparklineFields = []string{"cpu", "mem_used", "mem_total", "disk_pct"}

// AppCounts counts a server's apps by their live status.
type AppCounts struct {
	Running     int `json:"running"`
	Stopped     int `json:"stopped"`
	Problematic int `json:"problematic"`
}

// ServerOverview is one dashboard health widget.
type ServerOverview struct {
	ServerID string          `json:"server_id"`
	Liveness status.Liveness `json:"liveness"`
	// Latest is the newest host sample; null before the first one arrives
	// (or after an hour without samples).
	Latest    *metrics.Latest `json:"latest"`
	Sparkline []metrics.Point `json:"sparkline"`
	Apps      AppCounts       `json:"apps"`
}

// OverviewResponse is the data of GET /api/v1/metrics/overview.
type OverviewResponse struct {
	Servers []ServerOverview `json:"servers"`
}

// countApps buckets live app statuses: active/restarting/idle run,
// stopped is stopped, problematic is problematic, unknown is not counted.
func countApps(apps []command.AppStatus) AppCounts {
	var c AppCounts
	for _, a := range apps {
		switch a.StatusCode {
		case command.ContainerStatusActive, command.ContainerStatusRestarting, command.ContainerStatusIdle:
			c.Running++
		case command.ContainerStatusStopped:
			c.Stopped++
		case command.ContainerStatusProblematic:
			c.Problematic++
		}
	}
	return c
}

// Overview serves GET /api/v1/metrics/overview: every server of the
// caller's organizations, one tier-1 query each.
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	userID, ok := util.RequireUser(w, r)
	if !ok {
		return
	}
	servers, err := h.servers.GetServers(r.Context(), userID)
	if err != nil {
		util.Error(w, "failed to load servers", nil)
		return
	}
	now := h.now()
	out := make([]ServerOverview, 0, len(servers))
	for _, s := range servers {
		o := ServerOverview{
			ServerID:  s.ID,
			Liveness:  h.status.ServerLiveness(s.ID, now),
			Sparkline: []metrics.Point{},
			Apps:      countApps(h.status.AppStatuses(s.ID, now)),
		}
		if l, ok := h.latest.Get(s.ID); ok {
			o.Latest = &l
		}
		rows, err := h.repo.Series(r.Context(), s.ID, "", metrics.Tier1, now.Add(-time.Hour))
		if err != nil {
			h.log.Error("metrics: overview sparkline", "error", err, "server_id", s.ID)
		}
		for _, p := range toPoints(rows) {
			o.Sparkline = append(o.Sparkline, p.Pick(sparklineFields...))
		}
		out = append(out, o)
	}
	util.Success(w, "ok", OverviewResponse{Servers: out})
}
```

- [ ] **Run the unit test, expect PASS**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/app/web/handler/monitoring/
```
Expected: `ok`.

- [ ] **Register the routes and write the contract test** (probes give `TestForeignServer` its 403 assertion per route and `TestMalformed` its 400. `TestMetricsRoutes` is the happy path the `TestMain` guard requires.)

Apply to `internal/app/web/routes.go`:

```diff
--- a/internal/app/web/routes.go
+++ b/internal/app/web/routes.go
@@ -10,6 +10,7 @@
 	happ "winterflow/internal/app/web/handler/app"
 	hauth "winterflow/internal/app/web/handler/auth"
 	hdocker "winterflow/internal/app/web/handler/docker"
+	"winterflow/internal/app/web/handler/monitoring"
 	"winterflow/internal/app/web/handler/notification"
 	horg "winterflow/internal/app/web/handler/org"
 	"winterflow/internal/app/web/handler/server"
@@ -88,6 +89,13 @@
 		StatusCache:      d.StatusCache,
 		Cfg:              d.Cfg,
 	})
+	monitoringAPI := monitoring.NewHandler(&monitoring.Deps{
+		Logger:      d.Log,
+		Servers:     d.ServerRepository,
+		Metrics:     d.MetricRepository,
+		Latest:      d.LatestMetrics,
+		StatusCache: d.StatusCache,
+	})
 	usersAPI := huser.NewHandler(&huser.Deps{Logger: d.Log, Tokens: d.UserService, Users: d.UserService})
 	orgAPI := horg.NewHandler(&horg.Deps{Logger: d.Log, Users: d.UserService})
 
@@ -101,6 +109,7 @@
 		appsAPI.Routes(g),
 		dockerAPI.Routes(g),
 		serversAPI.Routes(g),
+		monitoringAPI.Routes(g),
 		usersAPI.Routes(g),
 		orgAPI.Routes(g),
 		authprvd.Routes(),
```

Apply to `internal/app/web/contract/matrix_test.go`:

```diff
--- a/internal/app/web/contract/matrix_test.go
+++ b/internal/app/web/contract/matrix_test.go
@@ -99,6 +99,13 @@
 		return call{Method: http.MethodPost, Path: "/api/v1/agent/update", Query: sq(s), Body: map[string]any{"version": "v1"}}
 	}, malformed: bad("POST", "/api/v1/agent/update")},
 
+	"GET /api/v1/metrics/server": {valid: func(s string) call {
+		return get("/api/v1/metrics/server", url.Values{"server_id": {s}, "range": {"1h"}})
+	}, malformed: bad("GET", "/api/v1/metrics/server")},
+	"GET /api/v1/metrics/app": {valid: func(s string) call {
+		return get("/api/v1/metrics/app", url.Values{"server_id": {s}, "app_id": {anApp}, "range": {"24h"}})
+	}, malformed: bad("GET", "/api/v1/metrics/app")},
+
 	"GET /api/v1/server/get-public-key": {valid: func(s string) call { return get("/api/v1/server/get-public-key", sq(s)) }, malformed: bad("GET", "/api/v1/server/get-public-key")},
 	"POST /api/v1/server/register":      {malformed: post("/api/v1/server/register", map[string]any{"code": "1"})},
 
```

`internal/app/web/contract/metrics_test.go`:

```go
package contract_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"winterflow/internal/app/web/handler/monitoring"
	"winterflow/internal/domain/metrics"
	"winterflow/internal/infra/transport/bus"
)

// TestMetricsRoutes: a server.metrics event on the events queue becomes a
// point on the server and app charts and the dashboard overview.
func TestMetricsRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)

	cpu, appCPU := 37.5, 4.0
	body, err := json.Marshal(metrics.Sample{
		TS: time.Now().Unix(),
		Server: metrics.ServerSample{CPU: &cpu, MemUsed: 2e9, MemTotal: 8e9,
			Disks: []metrics.Disk{{Mount: "/", Used: 40, Total: 100}}},
		Apps: []metrics.AppSample{{AppID: anApp, CPU: &appCPU}},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.publishEvent(t, bus.EventMessage{ServerID: sid, Kind: bus.EventServerMetrics, Payload: body})

	q := url.Values{"server_id": {sid}, "range": {"1h"}}
	var series monitoring.ServerSeriesResponse
	eventually(t, func() bool {
		owner.get(t, "/api/v1/metrics/server", q).expect(t, http.StatusOK).data(t, &series)
		return len(series.Points) == 1
	})
	if p := series.Points[0]; p["cpu"] != 37.5 || p["cpu_max"] != 37.5 || p["disk_pct"] != 40 || series.IntervalSeconds != 60 {
		t.Fatalf("server series = %+v", series)
	}
	// The app is unknown to the API (never saved), so it has no rows.
	if len(series.Apps) != 0 {
		t.Fatalf("apps = %+v", series.Apps)
	}

	var app monitoring.SeriesResponse
	owner.get(t, "/api/v1/metrics/app", url.Values{"server_id": {sid}, "app_id": {anApp}, "range": {"30d"}}).
		expect(t, http.StatusOK).data(t, &app)
	if app.IntervalSeconds != 28800 || len(app.Points) != 0 {
		t.Fatalf("app series = %+v", app)
	}

	var ov monitoring.OverviewResponse
	owner.get(t, "/api/v1/metrics/overview", nil).expect(t, http.StatusOK).data(t, &ov)
	if len(ov.Servers) != 1 || ov.Servers[0].ServerID != sid || ov.Servers[0].Latest == nil ||
		*ov.Servers[0].Latest.Server.CPU != 37.5 || len(ov.Servers[0].Sparkline) != 1 {
		t.Fatalf("overview = %+v", ov)
	}

	// An unknown range is outside the documented enum: 400, not a guess.
	owner.do(t, call{Method: http.MethodGet, Path: "/api/v1/metrics/server", Invalid: true,
		Query: url.Values{"server_id": {sid}, "range": {"2h"}}}).expect(t, http.StatusBadRequest)
}
```

- [ ] **Run the contract suite, expect FAIL until the spec is regenerated**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/app/web/...
```
Expected: `TestOpenAPIUpToDate` and `TestRoutesGolden` fail (the new routes are missing from `api/openapi.yaml` and `routes.golden`), and the contract tests fail spec validation.

- [ ] **Regenerate the spec and route golden, expect PASS on both engines**

```bash
GOTMPDIR=$HOME/.cache/wf-gotmp make openapi
GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/app/web -run TestRoutesGolden -update
WF_CONTRACT_GUARD=require GOTMPDIR=$HOME/.cache/wf-gotmp go test ./internal/app/web/...
GOTMPDIR=$HOME/.cache/wf-gotmp make test-pg TEST_PG_ARGS='-run "Metrics|Foreign|Malformed|Unauthenticated|Probes|Parity"'
```
Expected: all `ok`. `git diff internal/app/web/testdata/routes.golden` shows exactly three added `GET /api/v1/metrics/...` lines, and `TestParity`'s golden is unchanged.

- [ ] **Commit**

```bash
git add internal/app/web api/openapi.yaml
git commit -m "feat(api): serve metrics charts and the dashboard overview" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 11: Web: metrics model, helpers and polling hooks

**Files:**
- Create: `web/src/lib/metrics.ts`, `web/src/hooks/use-metrics.ts`
- Test: `web/src/lib/metrics.test.ts`, `web/src/hooks/use-metrics.test.ts`

**Interfaces:**
- Consumes: `apiBaseUrl` (`@/config`), `readApiError` (`@/lib/api`); the JSON shapes of Task 10.
- Produces (`@/lib/metrics`): types `MetricsRange`, `MetricsMode` (`"avg" | "peak"`), `MetricPoint`, `Series`, `AppSeries`, `ServerSeries`, `Disk`, `DockerDF`, `ServerSample`, `AppCounts`, `ServerOverview`, `ChartRow`; `METRICS_RANGES`; `TEMP_PREFIX`; `fieldKey(field, mode)`; `toChartRows(points, intervalSeconds, fields, mode): ChartRow[]` (t in ms, with a null row inside every gap > 1.5 intervals); `hasData`; `sensorFields`; `stackApps(apps, field, mode)`; `lastPoint`; `formatSize`, `formatRate`, `formatPercent`, `formatTemp`, `percentOf`, `fullestDisk`, `formatTick(ms, range)`; `chartColor(i)`.
- Produces (`@/hooks/use-metrics`): `METRICS_REFRESH_MS = 60_000`; `type MetricsState<T> = { data: T | null; loading; error: string | null; refresh }`; `usePolled<T>(path | null)`; `useServerMetrics(serverId | null, range)`; `useAppMetrics(serverId | null, appId | null, range)`; `useMetricsOverview()`.

- [ ] **Write the failing tests**

`web/src/lib/metrics.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import {
  fieldKey,
  formatPercent,
  formatRate,
  formatSize,
  formatTemp,
  fullestDisk,
  hasData,
  lastPoint,
  percentOf,
  sensorFields,
  stackApps,
  toChartRows,
} from "@/lib/metrics";

describe("toChartRows", () => {
  it("reads averages or peaks and converts t to milliseconds", () => {
    const pts = [{ t: 60, cpu: 10, cpu_max: 40 }, { t: 120, cpu: 20, cpu_max: 25 }];
    expect(toChartRows(pts, 60, ["cpu"], "avg")).toEqual([
      { t: 60_000, cpu: 10 },
      { t: 120_000, cpu: 20 },
    ]);
    expect(toChartRows(pts, 60, ["cpu"], "peak").map((r) => r.cpu)).toEqual([40, 25]);
  });

  it("breaks the line over a gap (server offline) with a null row", () => {
    const pts = [{ t: 0, cpu: 1 }, { t: 60, cpu: 2 }, { t: 600, cpu: 3 }];
    const rows = toChartRows(pts, 60, ["cpu"], "avg");
    expect(rows).toHaveLength(4);
    expect(rows[2]).toEqual({ t: 120_000, cpu: null });
    // A slightly late sample is not a gap.
    expect(toChartRows([{ t: 0, cpu: 1 }, { t: 85, cpu: 2 }], 60, ["cpu"], "avg")).toHaveLength(2);
  });

  it("maps missing fields (first sample has no rates) to null", () => {
    expect(toChartRows([{ t: 0, mem_used: 5 }], 60, ["cpu"], "avg")).toEqual([{ t: 0, cpu: null }]);
  });
});

describe("helpers", () => {
  it("fieldKey", () => {
    expect(fieldKey("cpu", "avg")).toBe("cpu");
    expect(fieldKey("cpu", "peak")).toBe("cpu_max");
  });

  it("hasData and lastPoint", () => {
    expect(hasData([], ["cpu"])).toBe(false);
    expect(hasData([{ t: 1, mem_used: 1 }], ["cpu"])).toBe(false);
    expect(hasData([{ t: 1, cpu: 0 }], ["cpu"])).toBe(true);
    expect(lastPoint([])).toBeUndefined();
    expect(lastPoint([{ t: 1 }, { t: 2 }])).toEqual({ t: 2 });
  });

  it("sensorFields lists sensors without peaks, sorted", () => {
    expect(
      sensorFields([{ t: 1, "temp:b": 1, "temp:b_max": 2, temp: 3 }, { t: 2, "temp:a": 1 }]),
    ).toEqual(["temp:a", "temp:b"]);
  });

  it("stackApps aligns apps on t", () => {
    const rows = stackApps(
      [
        { app_id: "a", points: [{ t: 60, cpu: 1, cpu_max: 2 }, { t: 0, cpu: 3 }] },
        { app_id: "b", points: [{ t: 60, cpu: 4 }, { t: 120, mem_used: 9 }] },
      ],
      "cpu",
      "avg",
    );
    expect(rows).toEqual([
      { t: 0, a: 3 },
      { t: 60_000, a: 1, b: 4 },
    ]);
  });

  it("formats sizes, rates, percents and temperatures", () => {
    expect(formatSize(0)).toBe("0 B");
    expect(formatSize(1536)).toBe("1.5 KB");
    expect(formatSize(5 * 1024 ** 3)).toBe("5.0 GB");
    expect(formatSize(undefined)).toBe("–");
    expect(formatRate(2048)).toBe("2.0 KB/s");
    expect(formatRate(undefined)).toBe("–");
    expect(formatPercent(4.25)).toBe("4.3%");
    expect(formatPercent(93.6)).toBe("94%");
    expect(formatPercent(Number.NaN)).toBe("–");
    expect(formatTemp(41.6)).toBe("42°C");
    expect(formatTemp(undefined)).toBe("–");
  });

  it("percentOf and fullestDisk", () => {
    expect(percentOf(1, 4)).toBe(25);
    expect(percentOf(1, 0)).toBeUndefined();
    expect(percentOf(undefined, 4)).toBeUndefined();
    const disks = [
      { mount: "/", used: 10, total: 100 },
      { mount: "/data", used: 90, total: 100 },
      { mount: "/empty", used: 0, total: 0 },
    ];
    expect(fullestDisk(disks)?.mount).toBe("/data");
    expect(fullestDisk(null)).toBeUndefined();
  });
});
```

`web/src/hooks/use-metrics.test.ts`:

```ts
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  METRICS_REFRESH_MS,
  useAppMetrics,
  useMetricsOverview,
  useServerMetrics,
} from "@/hooks/use-metrics";

let urls: string[] = [];
const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  urls.push(String(input));
  return new Response(JSON.stringify({ data: { interval_seconds: 60, points: [], apps: [] } }), {
    status: 200,
  });
});

describe("metrics hooks", () => {
  beforeEach(() => {
    urls = [];
    fetchMock.mockClear();
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("loads a server's series with its range", async () => {
    const { result } = renderHook(() => useServerMetrics("srv-1", "24h"));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(urls[0]).toContain("/api/v1/metrics/server?server_id=srv-1&range=24h");
    expect(result.current.data?.interval_seconds).toBe(60);
    expect(result.current.error).toBeNull();
  });

  it("does nothing until a server and app are known", () => {
    const { result } = renderHook(() => useAppMetrics("srv-1", null, "1h"));
    expect(fetchMock).not.toHaveBeenCalled();
    expect(result.current.loading).toBe(false);
  });

  it("loads an app's series", async () => {
    const { result } = renderHook(() => useAppMetrics("srv-1", "app-1", "7d"));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(urls[0]).toContain("/api/v1/metrics/app?server_id=srv-1&app_id=app-1&range=7d");
  });

  it("refreshes every minute while visible and surfaces errors", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { result } = renderHook(() => useMetricsOverview());
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(urls[0]).toContain("/api/v1/metrics/overview");

    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ success: false, message: "forbidden" }), { status: 403 }),
    );
    await act(async () => {
      vi.advanceTimersByTime(METRICS_REFRESH_MS);
    });
    await waitFor(() => expect(result.current.error).toBe("forbidden"));

    // Hidden tab: the timer fires but nothing is fetched.
    const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    const before = fetchMock.mock.calls.length;
    await act(async () => {
      vi.advanceTimersByTime(METRICS_REFRESH_MS);
    });
    expect(fetchMock.mock.calls.length).toBe(before);
    visibility.mockRestore();
  });
});
```

- [ ] **Run them, expect FAIL**

```bash
pnpm --dir web exec vitest run src/lib/metrics.test.ts src/hooks/use-metrics.test.ts
```
Expected: `Failed to resolve import "@/lib/metrics"`.

- [ ] **Implement**

`web/src/lib/metrics.ts`:

```ts
// Monitoring data model and pure helpers shared by the dashboard widgets,
// the server Monitoring page and the app Metrics tab. Field names mirror
// the Go flattening in internal/domain/metrics (ServerSample.Values,
// AppSample.Values); every field has a "<field>_max" peak on chart points.

export type MetricsRange = "1h" | "12h" | "24h" | "7d" | "30d";

export const METRICS_RANGES: { value: MetricsRange; label: string }[] = [
  { value: "1h", label: "1 hour" },
  { value: "12h", label: "12 hours" },
  { value: "24h", label: "24 hours" },
  { value: "7d", label: "7 days" },
  { value: "30d", label: "30 days" },
];

// "avg" plots each point's average, "peak" its "<field>_max".
export type MetricsMode = "avg" | "peak";

// A chart point: "t" (unix seconds) plus field averages and peaks.
export type MetricPoint = Record<string, number>;

export type Series = { interval_seconds: number; points: MetricPoint[] };

export type AppSeries = { app_id: string; points: MetricPoint[] };

export type ServerSeries = Series & { apps: AppSeries[] };

export type Disk = { mount: string; used: number; total: number };

export type DockerDF = {
  images: number;
  volumes: number;
  build_cache: number;
  reclaimable: number;
};

// ServerSample mirrors metrics.ServerSample (the latest host sample).
export type ServerSample = {
  cpu?: number;
  mem_used: number;
  mem_total: number;
  swap_used: number;
  swap_total: number;
  load1: number;
  load5: number;
  load15: number;
  disks: Disk[] | null;
  disk_read_bps?: number;
  disk_write_bps?: number;
  net_rx_bps?: number;
  net_tx_bps?: number;
  temp?: number;
  temps?: Record<string, number>;
  docker_df?: DockerDF;
};

export type AppCounts = { running: number; stopped: number; problematic: number };

export type ServerOverview = {
  server_id: string;
  liveness: "online" | "unknown";
  latest: { at: string; server: ServerSample } | null;
  sparkline: MetricPoint[];
  apps: AppCounts;
};

// TEMP_PREFIX prefixes one sensor's field ("temp:Composite").
export const TEMP_PREFIX = "temp:";

// fieldKey is the point key a mode reads for field.
export function fieldKey(field: string, mode: MetricsMode): string {
  return mode === "peak" ? `${field}_max` : field;
}

// ChartRow is a point flattened for recharts: "t" in milliseconds, values
// keyed by field; null breaks the line.
export type ChartRow = { t: number } & Record<string, number | null>;

// toChartRows maps points to recharts rows for the given fields and mode,
// inserting a null row inside every gap longer than 1.5 intervals so the
// lines break where the target did not report instead of bridging it.
export function toChartRows(
  points: MetricPoint[],
  intervalSeconds: number,
  fields: string[],
  mode: MetricsMode,
): ChartRow[] {
  const rows: ChartRow[] = [];
  let prev: number | null = null;
  for (const p of points) {
    const t = p.t;
    if (prev !== null && intervalSeconds > 0 && t - prev > intervalSeconds * 1.5) {
      const gap: ChartRow = { t: (prev + intervalSeconds) * 1000 };
      for (const f of fields) gap[f] = null;
      rows.push(gap);
    }
    const row: ChartRow = { t: t * 1000 };
    for (const f of fields) {
      const v = p[fieldKey(f, mode)];
      row[f] = typeof v === "number" ? v : null;
    }
    rows.push(row);
    prev = t;
  }
  return rows;
}

// hasData reports whether any point carries any of the fields.
export function hasData(points: MetricPoint[], fields: string[]): boolean {
  return points.some((p) => fields.some((f) => typeof p[f] === "number"));
}

// sensorFields lists the per-sensor temperature fields present in points.
export function sensorFields(points: MetricPoint[]): string[] {
  const out = new Set<string>();
  for (const p of points) {
    for (const k of Object.keys(p)) {
      if (k.startsWith(TEMP_PREFIX) && !k.endsWith("_max")) out.add(k);
    }
  }
  return [...out].sort();
}

// stackApps turns per-app series into rows keyed by app id for one field,
// aligned on t (an app without a point at t contributes nothing there).
export function stackApps(
  apps: AppSeries[],
  field: string,
  mode: MetricsMode,
): ChartRow[] {
  const byT = new Map<number, ChartRow>();
  for (const a of apps) {
    for (const p of a.points) {
      const v = p[fieldKey(field, mode)];
      if (typeof v !== "number") continue;
      const row = byT.get(p.t) ?? { t: p.t * 1000 };
      row[a.app_id] = v;
      byT.set(p.t, row);
    }
  }
  return [...byT.values()].sort((x, y) => x.t - y.t);
}

// lastPoint is the newest point, or undefined.
export function lastPoint(points: MetricPoint[]): MetricPoint | undefined {
  return points.length ? points[points.length - 1] : undefined;
}

const UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

// formatSize renders bytes with a binary unit and one decimal ("1.5 GB").
export function formatSize(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes)) return "–";
  let v = Math.max(0, bytes);
  let i = 0;
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? v.toFixed(0) : v.toFixed(1)} ${UNITS[i]}`;
}

// formatRate renders bytes per second ("1.2 MB/s").
export function formatRate(bps: number | null | undefined): string {
  const s = formatSize(bps);
  return s === "–" ? s : `${s}/s`;
}

// formatPercent renders a percentage with one decimal under 10 %.
export function formatPercent(v: number | null | undefined): string {
  if (v === null || v === undefined || !Number.isFinite(v)) return "–";
  return `${v < 10 ? v.toFixed(1) : Math.round(v)}%`;
}

// formatTemp renders °C.
export function formatTemp(v: number | null | undefined): string {
  if (v === null || v === undefined || !Number.isFinite(v)) return "–";
  return `${Math.round(v)}°C`;
}

// percentOf is used/total in percent, undefined without a total.
export function percentOf(used: number | undefined, total: number | undefined): number | undefined {
  if (used === undefined || !total) return undefined;
  return (100 * used) / total;
}

// fullestDisk is the filesystem with the highest usage percentage.
export function fullestDisk(disks: Disk[] | null | undefined): Disk | undefined {
  let best: Disk | undefined;
  for (const d of disks ?? []) {
    if (!d.total) continue;
    if (!best || d.used / d.total > best.used / best.total) best = d;
  }
  return best;
}

// formatTick renders an x-axis tick for the range (time of day for short
// ranges, date for long ones).
export function formatTick(ms: number, range: MetricsRange): string {
  const d = new Date(ms);
  if (range === "7d" || range === "30d") {
    return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  }
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

const CHART_COLORS = ["var(--chart-1)", "var(--chart-2)", "var(--chart-3)", "var(--chart-4)", "var(--chart-5)"];

// chartColor cycles the theme's five chart colors.
export function chartColor(i: number): string {
  return CHART_COLORS[i % CHART_COLORS.length];
}
```

`web/src/hooks/use-metrics.ts`:

```ts
import { useCallback, useEffect, useState } from "react";

import { apiBaseUrl } from "@/config";
import { readApiError } from "@/lib/api";
import type {
  MetricsRange,
  Series,
  ServerOverview,
  ServerSeries,
} from "@/lib/metrics";

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

// METRICS_REFRESH_MS matches the agent's sampling period: refreshing faster
// would only refetch the same points.
export const METRICS_REFRESH_MS = 60_000;

export type MetricsState<T> = {
  data: T | null;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
};

async function getData<T>(path: string): Promise<T> {
  const res = await fetch(`${base}${path}`, { credentials: "include" });
  if (!res.ok) throw await readApiError(res);
  const body = (await res.json()) as { data: T };
  return body.data;
}

// usePolled fetches path now, then every METRICS_REFRESH_MS while the tab
// is visible (and once more when it becomes visible again). A null path
// (nothing selected yet) fetches nothing. Metrics are never pushed over SSE.
export function usePolled<T>(path: string | null): MetricsState<T> {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(path !== null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!path) return;
    try {
      const next = await getData<T>(path);
      setData(next);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load metrics");
    } finally {
      setLoading(false);
    }
  }, [path]);

  useEffect(() => {
    if (!path) return;
    void refresh();
    const tick = () => {
      if (document.visibilityState === "visible") void refresh();
    };
    const id = window.setInterval(tick, METRICS_REFRESH_MS);
    document.addEventListener("visibilitychange", tick);
    return () => {
      window.clearInterval(id);
      document.removeEventListener("visibilitychange", tick);
    };
  }, [path, refresh]);

  return { data, loading, error, refresh };
}

function query(params: Record<string, string>): string {
  return new URLSearchParams(params).toString();
}

// useServerMetrics loads GET /api/v1/metrics/server for one server.
export function useServerMetrics(serverId: string | null, range: MetricsRange) {
  return usePolled<ServerSeries>(
    serverId ? `/api/v1/metrics/server?${query({ server_id: serverId, range })}` : null,
  );
}

// useAppMetrics loads GET /api/v1/metrics/app for one app.
export function useAppMetrics(
  serverId: string | null,
  appId: string | null,
  range: MetricsRange,
) {
  return usePolled<Series>(
    serverId && appId
      ? `/api/v1/metrics/app?${query({ server_id: serverId, app_id: appId, range })}`
      : null,
  );
}

// useMetricsOverview loads the dashboard overview of every server.
export function useMetricsOverview() {
  return usePolled<{ servers: ServerOverview[] | null }>("/api/v1/metrics/overview");
}
```

- [ ] **Run them, expect PASS**

```bash
pnpm --dir web exec vitest run src/lib/metrics.test.ts src/hooks/use-metrics.test.ts
```
Expected: 2 files, 13 tests passed.

- [ ] **Commit**

```bash
git add web/src/lib/metrics.ts web/src/lib/metrics.test.ts web/src/hooks/use-metrics.ts web/src/hooks/use-metrics.test.ts
git commit -m "feat(web): add the metrics model, chart helpers and polling hooks" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 12: Web: chart card, sparkline, range and mode controls

**Files:**
- Create: `web/src/components/metric-chart.tsx`, `web/src/components/sparkline.tsx`
- Test: `web/src/components/metric-chart.test.tsx`

**Interfaces:**
- Consumes: `ChartContainer`, `ChartTooltip`, `ChartTooltipContent`, `ChartLegend`, `ChartLegendContent`, `ChartConfig` (vendored `@/components/ui/chart`); `Card*`; `ToggleGroup`, `ToggleGroupItem`; `METRICS_RANGES`, `formatTick`, `chartColor`, `ChartRow`, `MetricsMode`, `MetricsRange` (Task 11).
- Produces: `type ChartSeries = { key: string; label: string; color: string }`; `MetricChartCard({ title, description?, rows, series, range, format, stacked?, empty? })` (shows "No data yet" when no row has a number for any series, and `empty` overrides it; root has `data-testid="chart-<title>"`); `RangeSelect({ value, onChange })` (radio items labelled by `METRICS_RANGES[].label`); `ModeToggle({ value, onChange })` (radios "Average" and "Peak"); `Sparkline({ rows, field, color, max? })` (renders nothing with fewer than 2 numeric points; `data-testid="sparkline-<field>"`).

- [ ] **Write the failing test**

`web/src/components/metric-chart.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { MetricChartCard, ModeToggle, RangeSelect } from "@/components/metric-chart";
import { chartColor } from "@/lib/metrics";

const series = [{ key: "cpu", label: "CPU", color: chartColor(0) }];

describe("MetricChartCard", () => {
  it("says No data yet without rows or with only nulls", () => {
    const { rerender } = render(<MetricChartCard title="CPU" rows={[]} series={series} range="1h" format={String} />);
    expect(screen.getByTestId("chart-CPU")).toHaveTextContent("No data yet");
    rerender(<MetricChartCard title="CPU" rows={[{ t: 0, cpu: null }]} series={series} range="1h" format={String} />);
    expect(screen.getByTestId("chart-CPU")).toHaveTextContent("No data yet");
  });

  it("shows an explicit empty message over the chart", () => {
    render(
      <MetricChartCard
        title="CPU"
        rows={[{ t: 0, cpu: 1 }]}
        series={series}
        range="1h"
        format={String}
        empty="Container metrics are unavailable on this host"
      />,
    );
    expect(screen.getByText("Container metrics are unavailable on this host")).toBeInTheDocument();
  });

  it("renders a chart container when there are rows", () => {
    const { container } = render(
      <MetricChartCard title="CPU" rows={[{ t: 0, cpu: 1 }, { t: 60_000, cpu: 2 }]} series={series} range="1h" format={String} />,
    );
    expect(screen.queryByText("No data yet")).not.toBeInTheDocument();
    expect(container.querySelector("[data-chart]")).not.toBeNull();
  });

  it("cycles theme colors", () => {
    expect(chartColor(0)).toBe("var(--chart-1)");
    expect(chartColor(5)).toBe("var(--chart-1)");
  });
});

describe("RangeSelect and ModeToggle", () => {
  it("report the picked value", async () => {
    const onRange = vi.fn();
    const onMode = vi.fn();
    render(
      <>
        <RangeSelect value="1h" onChange={onRange} />
        <ModeToggle value="avg" onChange={onMode} />
      </>,
    );
    await userEvent.click(screen.getByRole("radio", { name: "7 days" }));
    expect(onRange).toHaveBeenCalledWith("7d");
    await userEvent.click(screen.getByRole("radio", { name: "Peak" }));
    expect(onMode).toHaveBeenCalledWith("peak");
    // Clicking the selected item would unset it; the value stays.
    await userEvent.click(screen.getByRole("radio", { name: "1 hour" }));
    expect(onRange).toHaveBeenCalledTimes(1);
  });
});
```

- [ ] **Run it, expect FAIL**

```bash
pnpm --dir web exec vitest run src/components/metric-chart.test.tsx
```
Expected: `Failed to resolve import "@/components/metric-chart"`.

- [ ] **Implement**

`web/src/components/metric-chart.tsx`:

```tsx
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  METRICS_RANGES,
  formatTick,
  type ChartRow,
  type MetricsMode,
  type MetricsRange,
} from "@/lib/metrics";

export type ChartSeries = { key: string; label: string; color: string };

type MetricChartCardProps = {
  title: string;
  description?: string;
  rows: ChartRow[];
  series: ChartSeries[];
  range: MetricsRange;
  format: (v: number) => string;
  // stacked draws the series on top of each other (per-app CPU/memory).
  stacked?: boolean;
  // empty replaces the chart with this message (no data yet, unavailable).
  empty?: string;
};

// MetricChartCard is one titled area chart. rows come from toChartRows or
// stackApps; a null value breaks the line (a gap where nothing reported).
export function MetricChartCard({
  title,
  description,
  rows,
  series,
  range,
  format,
  stacked,
  empty,
}: MetricChartCardProps) {
  const config: ChartConfig = Object.fromEntries(
    series.map((s) => [s.key, { label: s.label, color: s.color }]),
  );
  // Rows whose series are all null (first minutes, rates not reported yet)
  // are no data either: never draw an empty axis.
  const hasValues = rows.some((r) => series.some((s) => typeof r[s.key] === "number"));
  const message = empty ?? (hasValues ? undefined : "No data yet");
  return (
    <Card data-testid={`chart-${title}`}>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">{title}</CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
      </CardHeader>
      <CardContent>
        {message ? (
          <div className="flex h-[180px] items-center justify-center text-sm text-muted-foreground">
            {message}
          </div>
        ) : (
          <ChartContainer config={config} className="aspect-auto h-[180px] w-full">
            <AreaChart data={rows} margin={{ left: 0, right: 8, top: 4 }}>
              <CartesianGrid vertical={false} />
              <XAxis
                dataKey="t"
                type="number"
                scale="time"
                domain={["dataMin", "dataMax"]}
                tickFormatter={(v: number) => formatTick(v, range)}
                tickLine={false}
                axisLine={false}
                minTickGap={32}
              />
              <YAxis tickFormatter={(v: number) => format(v)} tickLine={false} axisLine={false} width={64} />
              <ChartTooltip
                content={
                  <ChartTooltipContent
                    labelFormatter={(_, payload) => {
                      const t = payload?.[0]?.payload?.t;
                      return typeof t === "number" ? new Date(t).toLocaleString() : "";
                    }}
                    formatter={(value, name) => (
                      <span>
                        {config[String(name)]?.label ?? name}: {format(Number(value))}
                      </span>
                    )}
                  />
                }
              />
              {series.length > 1 ? <ChartLegend content={<ChartLegendContent />} /> : null}
              {series.map((s) => (
                <Area
                  key={s.key}
                  dataKey={s.key}
                  type="monotone"
                  stroke={`var(--color-${s.key})`}
                  fill={`var(--color-${s.key})`}
                  fillOpacity={stacked ? 0.4 : 0.15}
                  stackId={stacked ? "stack" : undefined}
                  connectNulls={false}
                  isAnimationActive={false}
                  dot={false}
                />
              ))}
            </AreaChart>
          </ChartContainer>
        )}
      </CardContent>
    </Card>
  );
}

// RangeSelect picks the chart range.
export function RangeSelect({
  value,
  onChange,
}: {
  value: MetricsRange;
  onChange: (r: MetricsRange) => void;
}) {
  return (
    <ToggleGroup
      type="single"
      variant="outline"
      size="sm"
      value={value}
      onValueChange={(v) => v && onChange(v as MetricsRange)}
      aria-label="Range"
    >
      {METRICS_RANGES.map((r) => (
        <ToggleGroupItem key={r.value} value={r.value} aria-label={r.label}>
          {r.value}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}

// ModeToggle switches between averages and peaks.
export function ModeToggle({
  value,
  onChange,
}: {
  value: MetricsMode;
  onChange: (m: MetricsMode) => void;
}) {
  return (
    <ToggleGroup
      type="single"
      variant="outline"
      size="sm"
      value={value}
      onValueChange={(v) => v && onChange(v as MetricsMode)}
      aria-label="Average or peak"
    >
      <ToggleGroupItem value="avg">Average</ToggleGroupItem>
      <ToggleGroupItem value="peak">Peak</ToggleGroupItem>
    </ToggleGroup>
  );
}
```

`web/src/components/sparkline.tsx`:

```tsx
import { Area, AreaChart, YAxis } from "recharts";

import { ChartContainer } from "@/components/ui/chart";
import type { ChartRow } from "@/lib/metrics";

// Sparkline is a tiny axis-less area chart of one field (rows from
// toChartRows). Nothing renders without at least two points.
export function Sparkline({
  rows,
  field,
  color,
  max,
}: {
  rows: ChartRow[];
  field: string;
  color: string;
  max?: number;
}) {
  if (rows.filter((r) => typeof r[field] === "number").length < 2) return null;
  return (
    <ChartContainer
      config={{ [field]: { color } }}
      className="aspect-auto h-8 w-full"
      data-testid={`sparkline-${field}`}
    >
      <AreaChart data={rows} margin={{ top: 2, bottom: 0, left: 0, right: 0 }}>
        <YAxis hide domain={[0, max ?? "auto"]} />
        <Area
          dataKey={field}
          type="monotone"
          stroke={`var(--color-${field})`}
          fill={`var(--color-${field})`}
          fillOpacity={0.15}
          isAnimationActive={false}
          connectNulls={false}
          dot={false}
        />
      </AreaChart>
    </ChartContainer>
  );
}
```

- [ ] **Run it, expect PASS**

```bash
pnpm --dir web exec vitest run src/components/metric-chart.test.tsx
```
Expected: 5 tests passed.

- [ ] **Commit**

```bash
git add web/src/components/metric-chart.tsx web/src/components/metric-chart.test.tsx web/src/components/sparkline.tsx
git commit -m "feat(web): add metric chart cards and range/mode controls" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 13: Web: dashboard control panel (server health widgets)

**Files:**
- Modify (rewrite): `web/src/components/server-cards.tsx` (`web/src/pages/home.tsx` keeps rendering `<ServerCards />` unchanged)
- Test: `web/src/components/server-cards.test.tsx`

**Interfaces:**
- Consumes: `useServers()` (`servers, activeServerId, setActiveServerId, statusByServer, loading`); `useMetricsOverview()` (Task 11); `Sparkline` (Task 12); `Progress` (vendored); `chartColor`, `formatPercent`, `formatTemp`, `fullestDisk`, `percentOf`, `toChartRows`, `ChartRow`, `ServerOverview` (Task 11); `useNavigate`.
- Produces: `ServerCards()` (same export as before). Each card has `title="<name>: online|offline"`, meters with `data-testid="meter-CPU|Memory|Disk"` (the Disk meter shows the fullest mount), load, temperature only when reported, and `data-testid="app-counts"`. Clicking sets the active server and navigates to `/monitoring`. The firing-alert badge and the "Active alerts" strip are PR 2.

- [ ] **Write the failing test**

`web/src/components/server-cards.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ServerCards } from "@/components/server-cards";
import type { Server } from "@/context/servers-context-base";
import type { ServerOverview } from "@/lib/metrics";

const setActiveServerId = vi.fn();
let servers: Server[] = [];
let overview: ServerOverview[] = [];
let statusByServer: Record<string, "online" | "unknown"> = {};

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ servers, activeServerId: "s1", setActiveServerId, statusByServer, loading: false }),
}));
vi.mock("@/hooks/use-metrics", () => ({
  useMetricsOverview: () => ({ data: { servers: overview }, loading: false, error: null, refresh: vi.fn() }),
}));

const server = (id: string, name: string, lastSeenAt: string | null = null): Server => ({
  id,
  organizationId: "o",
  name,
  createdAt: "",
  lastSeenAt,
  capabilities: {},
});

function renderCards() {
  return render(
    <MemoryRouter>
      <Routes>
        <Route path="/" element={<ServerCards />} />
        <Route path="/monitoring" element={<div>monitoring page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ServerCards", () => {
  beforeEach(() => {
    setActiveServerId.mockClear();
    servers = [server("s1", "prod-1"), server("s2", "backup", "2026-10-11T10:00:00Z")];
    statusByServer = { s1: "online" };
    overview = [
      {
        server_id: "s1",
        liveness: "online",
        latest: {
          at: "2026-10-11T12:00:00Z",
          server: {
            cpu: 42,
            mem_used: 3,
            mem_total: 4,
            swap_used: 0,
            swap_total: 0,
            load1: 0.5,
            load5: 0.25,
            load15: 0.125,
            disks: [
              { mount: "/", used: 10, total: 100 },
              { mount: "/srv", used: 90, total: 100 },
            ],
            temp: 55.4,
          },
        },
        sparkline: [{ t: 0, cpu: 10, mem_used: 1, mem_total: 4, disk_pct: 90 }],
        apps: { running: 3, stopped: 1, problematic: 1 },
      },
    ];
  });

  it("shows meters, load, temperature and app counts", () => {
    renderCards();
    const card = screen.getByTitle("prod-1: online");
    expect(within(card).getByTestId("meter-CPU")).toHaveTextContent("42%");
    expect(within(card).getByTestId("meter-Memory")).toHaveTextContent("75%");
    // The fullest filesystem, labelled by its mount.
    expect(within(card).getByTestId("meter-Disk")).toHaveTextContent("90%/srv");
    expect(card).toHaveTextContent("Load 0.50 / 0.25 / 0.13");
    expect(card).toHaveTextContent("55°C");
    expect(within(card).getByTestId("app-counts")).toHaveTextContent("3 running1 stopped1 problematic");
  });

  it("says when a server is offline and has no data", () => {
    renderCards();
    const card = screen.getByTitle("backup: offline");
    expect(card).toHaveTextContent("Offline since");
    expect(card).toHaveTextContent("No data yet");
  });

  it("hides temperature without sensors and problematic when zero", () => {
    overview[0].latest!.server.temp = undefined;
    overview[0].apps.problematic = 0;
    renderCards();
    const card = screen.getByTitle("prod-1: online");
    expect(card).not.toHaveTextContent("°C");
    expect(card).not.toHaveTextContent("problematic");
  });

  it("opens the server's Monitoring page on click", async () => {
    renderCards();
    await userEvent.click(screen.getByTitle("backup: offline"));
    expect(setActiveServerId).toHaveBeenCalledWith("s2");
    expect(await screen.findByText("monitoring page")).toBeInTheDocument();
  });

  it("renders nothing without servers", () => {
    servers = [];
    const { container } = renderCards();
    expect(container).toBeEmptyDOMElement();
  });
});
```

- [ ] **Run it, expect FAIL**

```bash
pnpm --dir web exec vitest run src/components/server-cards.test.tsx
```
Expected: failures such as `Unable to find an element by: [data-testid="meter-CPU"]` (the old cards have no meters).

- [ ] **Implement** (replace the whole file)

`web/src/components/server-cards.tsx`:

```tsx
import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { Server as ServerIcon } from "lucide-react";

import { Card, CardContent } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { Sparkline } from "@/components/sparkline";
import { useServers } from "@/context/use-servers";
import type { Server } from "@/context/servers-context-base";
import { useMetricsOverview } from "@/hooks/use-metrics";
import {
  chartColor,
  formatPercent,
  formatTemp,
  fullestDisk,
  percentOf,
  toChartRows,
  type ChartRow,
  type ServerOverview,
} from "@/lib/metrics";
import { cn } from "@/lib/utils";

const SPARK_INTERVAL = 60; // the overview sparkline is tier 1: one point a minute

function formatSince(iso: string | null): string {
  if (!iso) return "never";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "never" : d.toLocaleString();
}

// Meter is one labelled usage bar with its 1-hour sparkline.
function Meter({
  label,
  percent,
  detail,
  rows,
  field,
  color,
}: {
  label: string;
  percent: number | undefined;
  detail?: string;
  rows: ChartRow[];
  field: string;
  color: string;
}) {
  return (
    <div className="space-y-1" data-testid={`meter-${label}`}>
      <div className="flex items-baseline justify-between text-xs">
        <span className="text-muted-foreground">{label}</span>
        <span className="font-medium tabular-nums">
          {formatPercent(percent)}
          {detail ? <span className="ml-1 font-normal text-muted-foreground">{detail}</span> : null}
        </span>
      </div>
      <Progress value={percent ?? 0} className="h-1.5" />
      <Sparkline rows={rows} field={field} color={color} max={100} />
    </div>
  );
}

// HealthCard is one server's dashboard widget.
function HealthCard({
  server,
  overview,
  online,
  selected,
  onOpen,
}: {
  server: Server;
  overview: ServerOverview | undefined;
  online: boolean;
  selected: boolean;
  onOpen: () => void;
}) {
  const latest = overview?.latest?.server;
  const rows = useMemo(() => {
    // Every sparkline is a percentage; memory's is derived from used/total.
    const pts = (overview?.sparkline ?? []).map((p) => {
      const mem = percentOf(p.mem_used, p.mem_total);
      return mem === undefined ? p : { ...p, mem_pct: mem };
    });
    return toChartRows(pts, SPARK_INTERVAL, ["cpu", "mem_pct", "disk_pct"], "avg");
  }, [overview?.sparkline]);
  const disk = fullestDisk(latest?.disks);
  const apps = overview?.apps;

  return (
    <Card
      title={`${server.name}: ${online ? "online" : "offline"}`}
      onClick={onOpen}
      className={cn(
        "w-[340px] cursor-pointer transition-all hover:bg-muted/50",
        selected && "border-primary ring-1 ring-primary",
      )}
    >
      <CardContent className="space-y-3 p-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span
                className={cn("size-2 shrink-0 rounded-full", online ? "bg-green-500" : "bg-gray-400")}
              />
              <span className="truncate font-medium">{server.name}</span>
            </div>
            <div className="mt-1 text-xs text-muted-foreground">
              {online
                ? `Online${server.capabilities.server_ip ? ` · ${server.capabilities.server_ip}` : ""}`
                : server.lastSeenAt
                  ? `Offline since ${formatSince(server.lastSeenAt)}`
                  : "Waiting for the agent to connect…"}
            </div>
          </div>
          <div
            className={cn(
              "flex size-10 shrink-0 items-center justify-center rounded-lg",
              online ? "bg-primary/10" : "bg-muted",
            )}
          >
            <ServerIcon className={cn("size-5", online ? "text-primary" : "text-muted-foreground")} />
          </div>
        </div>

        {latest ? (
          <div className="space-y-2">
            <Meter label="CPU" percent={latest.cpu} rows={rows} field="cpu" color={chartColor(0)} />
            <Meter
              label="Memory"
              percent={percentOf(latest.mem_used, latest.mem_total)}
              rows={rows}
              field="mem_pct"
              color={chartColor(1)}
            />
            <Meter
              label="Disk"
              percent={disk ? percentOf(disk.used, disk.total) : undefined}
              detail={disk?.mount}
              rows={rows}
              field="disk_pct"
              color={chartColor(2)}
            />
            <div className="flex flex-wrap gap-x-3 text-xs text-muted-foreground">
              <span>
                Load {latest.load1.toFixed(2)} / {latest.load5.toFixed(2)} / {latest.load15.toFixed(2)}
              </span>
              {latest.temp !== undefined ? <span>{formatTemp(latest.temp)}</span> : null}
            </div>
          </div>
        ) : (
          <div className="text-xs text-muted-foreground">No data yet</div>
        )}

        {apps ? (
          <div className="flex gap-3 text-xs" data-testid="app-counts">
            <span>{apps.running} running</span>
            <span className="text-muted-foreground">{apps.stopped} stopped</span>
            {apps.problematic > 0 ? (
              <span className="text-destructive">{apps.problematic} problematic</span>
            ) : null}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

// ServerCards renders the org's servers as health widgets at the top of the
// dashboard: liveness, CPU/memory/disk meters with 1-hour sparklines, load,
// temperature and app counts. Clicking one makes it the active server and
// opens its Monitoring page.
export function ServerCards() {
  const { servers, activeServerId, setActiveServerId, statusByServer, loading } = useServers();
  const { data } = useMetricsOverview();
  const navigate = useNavigate();

  const byId = useMemo(() => {
    const m = new Map<string, ServerOverview>();
    for (const o of data?.servers ?? []) m.set(o.server_id, o);
    return m;
  }, [data]);

  if (loading && servers.length === 0) {
    return <div className="bg-muted/50 h-[220px] w-[340px] rounded-xl" />;
  }
  if (servers.length === 0) {
    return null;
  }

  return (
    <div className="flex flex-wrap gap-4">
      {servers.map((s) => (
        <HealthCard
          key={s.id}
          server={s}
          overview={byId.get(s.id)}
          online={statusByServer[s.id] === "online"}
          selected={s.id === activeServerId}
          onOpen={() => {
            setActiveServerId(s.id);
            navigate("/monitoring");
          }}
        />
      ))}
    </div>
  );
}
```

- [ ] **Run it, expect PASS**

```bash
pnpm --dir web exec vitest run src/components/server-cards.test.tsx
```
Expected: 5 tests passed.

- [ ] **Commit**

```bash
git add web/src/components/server-cards.tsx web/src/components/server-cards.test.tsx
git commit -m "feat(web): turn dashboard server cards into health widgets" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 14: Web: server Monitoring page, route and sidebar entry

**Files:**
- Create: `web/src/pages/monitoring.tsx`, `web/src/lib/nav.ts`
- Modify: `web/src/components/app-sidebar.tsx` (navMain moves to `lib/nav.ts`; a component file may not export a constant object), `web/src/main.tsx` (route `/monitoring`)
- Test: `web/src/pages/monitoring.test.tsx`, `web/src/lib/nav.test.ts`

**Interfaces:**
- Consumes: `useServers()` (`activeServer`, `activeServerId`), `useApps()` (`apps` for app names), `useServerMetrics` (Task 11), `MetricChartCard`, `RangeSelect`, `ModeToggle`, `ChartSeries` (Task 12), `useAppBreadcrumbs`.
- Produces: default export `MonitoringPage` (charts CPU, Memory (memory + swap %), Load, Disk usage (fullest %), Disk I/O, Network, Temperature (hidden without sensors), CPU by app and Memory by app (stacked), and a Docker disk usage card with `data-testid="docker-df"`; default range `24h`, mode `avg`); `navMain` in `@/lib/nav` with Dashboard and Monitoring. The Alerts card is PR 2.

- [ ] **Write the failing tests**

`web/src/pages/monitoring.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Outlet, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import MonitoringPage from "@/pages/monitoring";
import type { ServerSeries } from "@/lib/metrics";

const useServerMetrics = vi.fn();
let data: ServerSeries | null = null;

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ activeServer: { id: "s1", name: "prod-1" }, activeServerId: "s1" }),
}));
vi.mock("@/context/use-apps", () => ({
  useApps: () => ({ apps: [{ id: "app-1", name: "Nextcloud" }] }),
}));
vi.mock("@/hooks/use-metrics", () => ({
  useServerMetrics: (...args: unknown[]) => {
    useServerMetrics(...args);
    return { data, loading: false, error: null, refresh: vi.fn() };
  },
}));

function renderPage() {
  return render(
    <MemoryRouter>
      <Routes>
        <Route element={<Outlet context={{ setBreadcrumbs: vi.fn() }} />}>
          <Route path="/" element={<MonitoringPage />} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe("MonitoringPage", () => {
  beforeEach(() => {
    useServerMetrics.mockClear();
    data = {
      interval_seconds: 60,
      points: [
        { t: 0, cpu: 5, mem_used: 1, mem_total: 4, load1: 1, "temp:Composite": 40 },
        {
          t: 60,
          cpu: 6,
          mem_used: 2,
          mem_total: 4,
          load1: 1,
          "temp:Composite": 41,
          docker_images: 1024 ** 3,
          docker_volumes: 0,
          docker_build_cache: 0,
          docker_reclaimable: 512,
        },
      ],
      apps: [{ app_id: "app-1", points: [{ t: 0, cpu: 1, mem_used: 5 }] }],
    };
  });

  it("charts the active server and lists Docker disk usage", () => {
    renderPage();
    expect(useServerMetrics).toHaveBeenLastCalledWith("s1", "24h");
    for (const title of ["CPU", "Memory", "Load", "Disk usage", "Disk I/O", "Network", "Temperature", "CPU by app", "Memory by app"]) {
      expect(screen.getByTestId(`chart-${title}`)).toBeInTheDocument();
    }
    expect(screen.getByTestId("docker-df")).toHaveTextContent("Images1.0 GB");
    expect(screen.getByTestId("docker-df")).toHaveTextContent("Reclaimable512 B");
    // Rates were never reported: that chart says so instead of drawing zeros.
    expect(screen.getByTestId("chart-Network")).toHaveTextContent("No data yet");
  });

  it("hides the temperature chart without sensors and waits for the disk scan", () => {
    data!.points = [{ t: 0, cpu: 1 }];
    renderPage();
    expect(screen.queryByTestId("chart-Temperature")).not.toBeInTheDocument();
    expect(screen.getByText(/No disk scan yet/)).toBeInTheDocument();
  });

  it("refetches for a new range", async () => {
    renderPage();
    await userEvent.click(screen.getByRole("radio", { name: "7 days" }));
    expect(useServerMetrics).toHaveBeenLastCalledWith("s1", "7d");
  });
});
```

`web/src/lib/nav.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import { navMain } from "@/lib/nav";

describe("navMain", () => {
  it("links the Monitoring page right after the Dashboard", () => {
    expect(navMain.map((i) => [i.title, i.url])).toEqual([
      ["Dashboard", "/"],
      ["Monitoring", "/monitoring"],
    ]);
  });
});
```

- [ ] **Run them, expect FAIL**

```bash
pnpm --dir web exec vitest run src/pages/monitoring.test.tsx src/lib/nav.test.ts
```
Expected: `Failed to resolve import "@/pages/monitoring"` and `"@/lib/nav"`.

- [ ] **Implement**

`web/src/pages/monitoring.tsx`:

```tsx
import { useMemo, useState } from "react";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { MetricChartCard, ModeToggle, RangeSelect, type ChartSeries } from "@/components/metric-chart";
import { useApps } from "@/context/use-apps";
import { useServers } from "@/context/use-servers";
import { useServerMetrics } from "@/hooks/use-metrics";
import {
  chartColor,
  formatPercent,
  formatRate,
  formatSize,
  formatTemp,
  lastPoint,
  sensorFields,
  stackApps,
  toChartRows,
  type MetricPoint,
  type MetricsMode,
  type MetricsRange,
} from "@/lib/metrics";

// withPercents adds memory and swap as percentages of their totals (avg and
// peak), so both chart on one 0-100 axis.
function withPercents(points: MetricPoint[]): MetricPoint[] {
  return points.map((p) => {
    const out = { ...p };
    if (p.mem_total) {
      out.mem_pct = (100 * p.mem_used) / p.mem_total;
      out.mem_pct_max = (100 * (p.mem_used_max ?? p.mem_used)) / p.mem_total;
    }
    if (p.swap_total) {
      out.swap_pct = (100 * p.swap_used) / p.swap_total;
      out.swap_pct_max = (100 * (p.swap_used_max ?? p.swap_used)) / p.swap_total;
    }
    return out;
  });
}

function s(key: string, label: string, i: number): ChartSeries {
  return { key, label, color: chartColor(i) };
}

// MonitoringPage charts the switcher's active server: host resources, the
// per-app CPU and memory stack, and Docker's disk usage.
export default function MonitoringPage() {
  const { activeServer, activeServerId } = useServers();
  const { apps } = useApps();
  const [range, setRange] = useState<MetricsRange>("24h");
  const [mode, setMode] = useState<MetricsMode>("avg");
  const { data, loading, error } = useServerMetrics(activeServerId, range);

  const breadcrumbs = useMemo(
    () => [{ label: "Monitoring", href: "#", hideOnMobile: true }, { label: activeServer?.name ?? "Server" }],
    [activeServer?.name],
  );
  useAppBreadcrumbs(breadcrumbs);

  const points = useMemo(() => withPercents(data?.points ?? []), [data]);
  const interval = data?.interval_seconds ?? 60;
  const rows = (fields: string[]) => toChartRows(points, interval, fields, mode);
  const sensors = sensorFields(points);
  const appName = (id: string) => apps.find((a) => a.id === id)?.name ?? id.slice(0, 8);
  const appSeries = (data?.apps ?? []).map((a, i) => s(a.app_id, appName(a.app_id), i));
  const last = lastPoint(points);

  if (!activeServerId) {
    return <p className="text-sm text-muted-foreground">No server selected.</p>;
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">{activeServer?.name ?? "Server"}</h1>
        <div className="flex flex-wrap gap-2">
          <RangeSelect value={range} onChange={setRange} />
          <ModeToggle value={mode} onChange={setMode} />
        </div>
      </div>
      {error ? <p className="text-sm text-destructive">{error}</p> : null}
      {loading && !data ? <p className="text-sm text-muted-foreground">Loading…</p> : null}

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        <MetricChartCard title="CPU" rows={rows(["cpu"])} series={[s("cpu", "CPU", 0)]} format={formatPercent} range={range} />
        <MetricChartCard
          title="Memory"
          rows={rows(["mem_pct", "swap_pct"])}
          series={[s("mem_pct", "Memory", 1), s("swap_pct", "Swap", 3)]}
          format={formatPercent}
          range={range}
        />
        <MetricChartCard
          title="Load"
          rows={rows(["load1", "load5", "load15"])}
          series={[s("load1", "1 min", 0), s("load5", "5 min", 1), s("load15", "15 min", 2)]}
          format={(v) => v.toFixed(2)}
          range={range}
        />
        <MetricChartCard
          title="Disk usage"
          description="Fullest filesystem"
          rows={rows(["disk_pct"])}
          series={[s("disk_pct", "Used", 2)]}
          format={formatPercent}
          range={range}
        />
        <MetricChartCard
          title="Disk I/O"
          rows={rows(["disk_read_bps", "disk_write_bps"])}
          series={[s("disk_read_bps", "Read", 0), s("disk_write_bps", "Write", 1)]}
          format={formatRate}
          range={range}
        />
        <MetricChartCard
          title="Network"
          rows={rows(["net_rx_bps", "net_tx_bps"])}
          series={[s("net_rx_bps", "In", 1), s("net_tx_bps", "Out", 3)]}
          format={formatRate}
          range={range}
        />
        {/* No temperature sensors: the chart is hidden, not empty. */}
        {sensors.length > 0 ? (
          <MetricChartCard
            title="Temperature"
            rows={rows(sensors)}
            series={sensors.map((k, i) => s(k, k.slice("temp:".length), i))}
            format={formatTemp}
            range={range}
          />
        ) : null}
        <MetricChartCard
          title="CPU by app"
          rows={stackApps(data?.apps ?? [], "cpu", mode)}
          series={appSeries}
          format={formatPercent}
          range={range}
          stacked
        />
        <MetricChartCard
          title="Memory by app"
          rows={stackApps(data?.apps ?? [], "mem_used", mode)}
          series={appSeries}
          format={formatSize}
          range={range}
          stacked
        />
      </div>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm font-medium">Docker disk usage</CardTitle>
        </CardHeader>
        <CardContent>
          {last && last.docker_images !== undefined ? (
            <dl className="grid grid-cols-2 gap-3 text-sm md:grid-cols-4" data-testid="docker-df">
              <div>
                <dt className="text-muted-foreground">Images</dt>
                <dd>{formatSize(last.docker_images)}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Volumes</dt>
                <dd>{formatSize(last.docker_volumes)}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Build cache</dt>
                <dd>{formatSize(last.docker_build_cache)}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Reclaimable</dt>
                <dd>{formatSize(last.docker_reclaimable)}</dd>
              </div>
            </dl>
          ) : (
            <p className="text-sm text-muted-foreground">
              No disk scan yet — it runs hourly (METRICS_DISK_INTERVAL).
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
```

`web/src/lib/nav.ts`:

```ts
import { Activity, LayoutDashboard } from "lucide-react";

// Platform navigation: Dashboard, the active server's Monitoring, then the
// live Apps group (NavApps); Server Settings is pinned to the bottom of the
// sidebar (app-sidebar.tsx) — mirroring v1's primary/secondary split.
export const navMain = [
  {
    title: "Dashboard",
    url: "/",
    icon: LayoutDashboard,
    isActive: true,
  },
  {
    // Charts of the server switcher's active server.
    title: "Monitoring",
    url: "/monitoring",
    icon: Activity,
  },
];
```

Apply to `web/src/components/app-sidebar.tsx`:

```diff
--- a/web/src/components/app-sidebar.tsx
+++ b/web/src/components/app-sidebar.tsx
@@ -1,13 +1,14 @@
 "use client"
 
 import * as React from "react"
-import { LayoutDashboard, Settings2 } from "lucide-react"
+import { Settings2 } from "lucide-react"
 
 import { NavMain } from "@/components/nav-main"
 import { NavApps } from "@/components/nav-apps"
 import { NavSecondary } from "@/components/nav-secondary"
 import { NavUser } from "@/components/nav-user"
 import { ServerSwitcher } from "@/components/server-switcher"
+import { navMain } from "@/lib/nav"
 import {
     Sidebar,
     SidebarContent,
@@ -16,19 +17,6 @@
     SidebarRail,
 } from "@/components/ui/sidebar"
 
-// Platform navigation: Dashboard + the live Apps group, with Server Settings
-// (Docker registries/networks + agent update) pinned to the bottom — mirroring
-// v1's primary/secondary split. Catalog and organization management remain out
-// of scope for v2.
-const navMain = [
-    {
-        title: "Dashboard",
-        url: "/",
-        icon: LayoutDashboard,
-        isActive: true,
-    },
-]
-
 const navSecondary = [
     {
         title: "Server Settings",
```

Apply to `web/src/main.tsx`:

```diff
--- a/web/src/main.tsx
+++ b/web/src/main.tsx
@@ -17,6 +17,7 @@
 import HomePage from "@/pages/home"
 import CreateAppPage from "@/pages/create-app"
 import AppDetailsPage from "@/pages/app-details"
+import MonitoringPage from "@/pages/monitoring"
 import SettingsPage from "@/pages/settings"
 import UserTokensPage from "@/pages/user-tokens"
 import UserPasswordPage from "@/pages/user-password"
@@ -46,6 +47,10 @@
         element: <AppDetailsPage />,
       },
       {
+        path: "/monitoring",
+        element: <MonitoringPage />,
+      },
+      {
         path: "/settings",
         element: <SettingsPage />,
       },
```

- [ ] **Run them, expect PASS, then lint and type-check**

```bash
pnpm --dir web exec vitest run src/pages/monitoring.test.tsx src/lib/nav.test.ts
pnpm --dir web run lint
pnpm --dir web exec tsc -b
```
Expected: 4 tests passed; lint and tsc print no errors.

- [ ] **Commit**

```bash
git add web/src/pages/monitoring.tsx web/src/pages/monitoring.test.tsx web/src/lib/nav.ts web/src/lib/nav.test.ts web/src/components/app-sidebar.tsx web/src/main.tsx
git commit -m "feat(web): add the server Monitoring page" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 15: Web: app header info blocks and Metrics tab

**Files:**
- Create: `web/src/components/app-metrics.tsx`
- Modify: `web/src/pages/app-details.tsx`
- Test: `web/src/components/app-metrics.test.tsx`

**Interfaces:**
- Consumes: `useAppMetrics` (Task 11), `MetricChartCard`, `RangeSelect`, `ModeToggle` (Task 12), `lastPoint`, `toChartRows`, the formatters and `chartColor` (Task 11); `App.serverId` and `App.id` from `useApps()`.
- Produces: `AppMetricBlocks({ serverId, appId })` (blocks `data-testid="block-CPU|Memory|Disk|Network"` from the newest `1h` point; renders nothing without data; with `unavailable` it shows "Container metrics are unavailable on this host"); `AppMetricsTab({ serverId, appId })` (CPU, Memory used vs limit, Network, Disk charts; default range `24h`). `app-details.tsx` adds tab `metrics` (`?tab=metrics`) after Logs and renders the blocks under the header. The firing-alert badge and the Alerts card are PR 2.

- [ ] **Write the failing test**

`web/src/components/app-metrics.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppMetricBlocks, AppMetricsTab } from "@/components/app-metrics";
import type { Series } from "@/lib/metrics";

const calls: unknown[][] = [];
let data: Series | null = null;

vi.mock("@/hooks/use-metrics", () => ({
  useAppMetrics: (...args: unknown[]) => {
    calls.push(args);
    return { data, loading: false, error: null, refresh: vi.fn() };
  },
}));

describe("AppMetricBlocks", () => {
  beforeEach(() => {
    calls.length = 0;
    data = {
      interval_seconds: 60,
      points: [
        { t: 0, cpu: 1 },
        { t: 60, cpu: 12.5, mem_used: 512 * 1024 ** 2, mem_limit: 1024 ** 3, disk: 2 * 1024 ** 3, image: 1024 ** 2, net_rx_bps: 1024, net_tx_bps: 1024 },
      ],
    };
  });

  it("shows the newest minute", () => {
    render(<AppMetricBlocks serverId="s1" appId="a1" />);
    expect(calls[0]).toEqual(["s1", "a1", "1h"]);
    expect(screen.getByTestId("block-CPU")).toHaveTextContent("13%");
    expect(screen.getByTestId("block-Memory")).toHaveTextContent("512.0 MBof 1.0 GB limit");
    expect(screen.getByTestId("block-Disk")).toHaveTextContent("2.0 GB+ 1.0 MB images");
    expect(screen.getByTestId("block-Network")).toHaveTextContent("2.0 KB/s");
  });

  it("says no limit, and dashes for fields not reported yet", () => {
    data!.points = [{ t: 0, mem_used: 1 }];
    render(<AppMetricBlocks serverId="s1" appId="a1" />);
    expect(screen.getByTestId("block-Memory")).toHaveTextContent("no limit");
    expect(screen.getByTestId("block-CPU")).toHaveTextContent("–");
    expect(screen.getByTestId("block-Network")).toHaveTextContent("–");
  });

  it("renders nothing without data and explains unavailable containers", () => {
    data = { interval_seconds: 60, points: [] };
    const { container, rerender } = render(<AppMetricBlocks serverId="s1" appId="a1" />);
    expect(container).toBeEmptyDOMElement();
    data = { interval_seconds: 60, points: [{ t: 0, unavailable: 1, unavailable_max: 1 }] };
    rerender(<AppMetricBlocks serverId="s1" appId="a2" />);
    expect(screen.getByText("Container metrics are unavailable on this host")).toBeInTheDocument();
  });
});

describe("AppMetricsTab", () => {
  beforeEach(() => {
    calls.length = 0;
    data = { interval_seconds: 1200, points: [{ t: 0, cpu: 1, disk: 5 }] };
  });

  it("charts the app over the chosen range", async () => {
    render(<AppMetricsTab serverId="s1" appId="a1" />);
    expect(calls.at(-1)).toEqual(["s1", "a1", "24h"]);
    for (const title of ["CPU", "Memory", "Network", "Disk"]) {
      expect(screen.getByTestId(`chart-${title}`)).toBeInTheDocument();
    }
    expect(screen.getByTestId("chart-Memory")).toHaveTextContent("No data yet");
    await userEvent.click(screen.getByRole("radio", { name: "30 days" }));
    expect(calls.at(-1)).toEqual(["s1", "a1", "30d"]);
  });

  it("explains unavailable container metrics but still charts disk", () => {
    data = { interval_seconds: 60, points: [{ t: 0, unavailable: 1, disk: 5 }] };
    render(<AppMetricsTab serverId="s1" appId="a1" />);
    expect(screen.getByTestId("chart-CPU")).toHaveTextContent("Container metrics are unavailable on this host");
    expect(screen.getByTestId("chart-Disk")).not.toHaveTextContent("unavailable");
  });
});
```

- [ ] **Run it, expect FAIL**

```bash
pnpm --dir web exec vitest run src/components/app-metrics.test.tsx
```
Expected: `Failed to resolve import "@/components/app-metrics"`.

- [ ] **Implement**

`web/src/components/app-metrics.tsx`:

```tsx
import { useMemo, useState } from "react";

import { Card, CardContent } from "@/components/ui/card";
import { MetricChartCard, ModeToggle, RangeSelect } from "@/components/metric-chart";
import { useAppMetrics } from "@/hooks/use-metrics";
import {
  chartColor,
  formatPercent,
  formatRate,
  formatSize,
  lastPoint,
  toChartRows,
  type MetricPoint,
  type MetricsMode,
  type MetricsRange,
} from "@/lib/metrics";

// UNAVAILABLE is shown when the host's cgroups resolve under no known
// layout (rootless Docker, exotic drivers): the agent sends
// containers_unavailable and stores "unavailable": 1.
const UNAVAILABLE = "Container metrics are unavailable on this host";

function unavailable(p: MetricPoint | undefined): boolean {
  return (p?.unavailable ?? 0) > 0;
}

function Block({ label, value, detail }: { label: string; value: string; detail?: string }) {
  return (
    <Card className="min-w-[150px] flex-1" data-testid={`block-${label}`}>
      <CardContent className="p-3">
        <div className="text-xs text-muted-foreground">{label}</div>
        <div className="text-lg font-semibold tabular-nums">{value}</div>
        {detail ? <div className="text-xs text-muted-foreground">{detail}</div> : null}
      </CardContent>
    </Card>
  );
}

// AppMetricBlocks is the app header's row of info blocks: CPU now, memory
// against its limit, disk and network rate, from the newest 1-minute point.
export function AppMetricBlocks({ serverId, appId }: { serverId: string; appId: string }) {
  const { data } = useAppMetrics(serverId, appId, "1h");
  const last = lastPoint(data?.points ?? []);
  if (!last) return null;
  if (unavailable(last)) {
    return <p className="text-sm text-muted-foreground">{UNAVAILABLE}</p>;
  }
  const net =
    last.net_rx_bps !== undefined || last.net_tx_bps !== undefined
      ? (last.net_rx_bps ?? 0) + (last.net_tx_bps ?? 0)
      : undefined;
  return (
    <div className="flex flex-wrap gap-3">
      <Block label="CPU" value={formatPercent(last.cpu)} detail="of host" />
      <Block
        label="Memory"
        value={formatSize(last.mem_used)}
        detail={last.mem_limit ? `of ${formatSize(last.mem_limit)} limit` : "no limit"}
      />
      <Block label="Disk" value={formatSize(last.disk)} detail={last.image ? `+ ${formatSize(last.image)} images` : undefined} />
      <Block label="Network" value={formatRate(net)} />
    </div>
  );
}

// AppMetricsTab is the app page's Metrics tab: CPU, memory, network and
// disk growth over the selected range.
export function AppMetricsTab({ serverId, appId }: { serverId: string; appId: string }) {
  const [range, setRange] = useState<MetricsRange>("24h");
  const [mode, setMode] = useState<MetricsMode>("avg");
  const { data, error } = useAppMetrics(serverId, appId, range);
  const points = useMemo(() => data?.points ?? [], [data]);
  const interval = data?.interval_seconds ?? 60;
  const rows = (fields: string[]) => toChartRows(points, interval, fields, mode);
  const blind = unavailable(lastPoint(points)) ? UNAVAILABLE : undefined;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap justify-end gap-2">
        <RangeSelect value={range} onChange={setRange} />
        <ModeToggle value={mode} onChange={setMode} />
      </div>
      {error ? <p className="text-sm text-destructive">{error}</p> : null}
      <div className="grid gap-4 md:grid-cols-2">
        <MetricChartCard
          title="CPU"
          description="Percent of the whole host"
          rows={rows(["cpu"])}
          series={[{ key: "cpu", label: "CPU", color: chartColor(0) }]}
          format={formatPercent}
          range={range}
          empty={blind}
        />
        <MetricChartCard
          title="Memory"
          rows={rows(["mem_used", "mem_limit"])}
          series={[
            { key: "mem_used", label: "Used", color: chartColor(1) },
            { key: "mem_limit", label: "Limit", color: chartColor(3) },
          ]}
          format={formatSize}
          range={range}
          empty={blind}
        />
        <MetricChartCard
          title="Network"
          rows={rows(["net_rx_bps", "net_tx_bps"])}
          series={[
            { key: "net_rx_bps", label: "In", color: chartColor(1) },
            { key: "net_tx_bps", label: "Out", color: chartColor(3) },
          ]}
          format={formatRate}
          range={range}
          empty={blind}
        />
        <MetricChartCard
          title="Disk"
          description="Volumes, app directory and writable layers (hourly)"
          rows={rows(["disk"])}
          series={[{ key: "disk", label: "Disk", color: chartColor(2) }]}
          format={formatSize}
          range={range}
        />
      </div>
    </div>
  );
}
```

Apply to `web/src/pages/app-details.tsx`:

```diff
--- a/web/src/pages/app-details.tsx
+++ b/web/src/pages/app-details.tsx
@@ -35,6 +35,7 @@
 import { LogsView, type LogLine } from "@/components/logs-view";
 import { AppEditorPanel } from "@/components/app-editor-panel";
 import { AppStatusIndicator } from "@/components/app-status-badge";
+import { AppMetricBlocks, AppMetricsTab } from "@/components/app-metrics";
 import { useApps } from "@/context/use-apps";
 import { useNotifications } from "@/context/use-notifications";
 import { useServers } from "@/context/use-servers";
@@ -49,7 +50,7 @@
 
 const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;
 
-const TABS = ["logs", "editor", "history", "settings"] as const;
+const TABS = ["logs", "metrics", "editor", "history", "settings"] as const;
 
 const tabTriggerClass =
   "-mb-px rounded-none border-b-2 border-transparent px-1 pt-1 pb-2.5 text-muted-foreground shadow-none hover:text-foreground data-[state=active]:border-foreground data-[state=active]:bg-transparent data-[state=active]:text-foreground data-[state=active]:shadow-none";
@@ -176,11 +177,16 @@
         </div>
       </header>
 
+      <AppMetricBlocks serverId={app.serverId} appId={app.id} />
+
       <Tabs value={tab} onValueChange={setTab}>
         <TabsList className="h-auto w-full justify-start gap-6 rounded-none border-b bg-transparent p-0">
           <TabsTrigger value="logs" className={tabTriggerClass}>
             Logs
           </TabsTrigger>
+          <TabsTrigger value="metrics" className={tabTriggerClass}>
+            Metrics
+          </TabsTrigger>
           <TabsTrigger value="editor" className={tabTriggerClass}>
             Configuration
           </TabsTrigger>
@@ -194,6 +200,9 @@
         <TabsContent value="logs" className="mt-6">
           <LogsTab appId={app.id} />
         </TabsContent>
+        <TabsContent value="metrics" className="mt-6">
+          <AppMetricsTab serverId={app.serverId} appId={app.id} />
+        </TabsContent>
         <TabsContent value="editor" className="mt-0">
           <AppEditorPanel appId={app.id} />
         </TabsContent>
```

- [ ] **Run the whole web suite, lint, type-check and build**

```bash
pnpm --dir web run test
pnpm --dir web run lint
pnpm --dir web run build
```
Expected: all test files pass (88 tests in the prototype), no lint errors, and the build succeeds.

- [ ] **Commit**

```bash
git add web/src/components/app-metrics.tsx web/src/components/app-metrics.test.tsx web/src/pages/app-details.tsx
git commit -m "feat(web): show app metrics in the header and a Metrics tab" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 16: Full verification and coverage gates

**Files:**
- Modify (only if CI's ratchet asks): `coverage-baseline.json`

**Interfaces:**
- Consumes: everything above. Produces nothing new.

- [ ] **Run every gate the way CI does**

```bash
mkdir -p $HOME/.cache/wf-gotmp
export GOTMPDIR=$HOME/.cache/wf-gotmp
gofmt -l ./internal ./pkg ./cmd          # expect no output
go vet ./...                             # expect no output
make sqlc-check                          # expect no output
make test                                # go test -race with coverage
make test-pg TEST_PG_ARGS='-race -covermode=atomic -coverpkg=./... -coverprofile=contract-postgres.out'
tail -n +2 contract-postgres.out >> coverage.out
make web-test
go run ./tools/diffcov --base main --min 80 --go coverage.out --lcov web/coverage/lcov.info
go run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info
WF_CONTRACT_GUARD=require go test ./internal/app/web/contract/
bash scripts/install_test.sh             # unchanged installer still passes
```

Expected: every command passes; diffcov reports ≥ 80 % for Go and web.

- [ ] **Smoke-test standalone by hand** (Docker host): `make standalone`, log in, wait 2 minutes, then check that the dashboard widgets show CPU/memory/disk, `/monitoring` draws CPU and memory, an app's Metrics tab shows its CPU/memory, and after the first disk scan (within ~1 min of boot) the Docker disk card and app Disk block fill in. Then set `METRICS_ENABLED=false`, restart, and confirm the log says `metrics disabled` and charts stay at "No data yet".

- [ ] **Commit only if the ratchet printed `::notice::raise coverage-baseline.json`**

```bash
git add coverage-baseline.json
git commit -m "chore: raise the coverage baseline" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
