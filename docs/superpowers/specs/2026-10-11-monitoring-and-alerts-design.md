# Monitoring charts and alerts — design

Date: 2026-10-11. Status: approved in conversation, awaiting spec review.

## Context

Winterflow has no runtime resource metrics. The agent sends static capability
totals once at registration (`system_cpu_cores`, `system_memory_total`,
`system_disk_total`) and an `apps.status` event every 30 s. Nothing sends
outbound notifications. The only server-health signal is the API's in-memory
liveness (`status.Cache`, 90 s TTL, swept every 15 s).

We want Beszel-style monitoring: charts per server and per app, health widgets
on the dashboard, and configurable alerts delivered to notification channels
that belong to the **organization**, not to a user.

Reference: Beszel (github.com/henrygd/beszel). We take its storage shape
(JSON row per target per tick, 1m → 10m → 20m → 120m → 480m rollups carrying
avg + max, tier-based retention), its alert evaluation (average over a
1–60 minute window, fire on crossing, send a resolve) and its channel
mechanism (Shoutrrr URLs with a Test button). We do not take its hub-pulled
polling, per-user channels, per-system-only alerts, Docker stats API calls per
container, or its 1 s realtime mode.

## Goals

1. Server and app metrics collected every 60 s at near-zero idle cost: no
   process forks in the 60 s path.
2. Charts for 1h / 12h / 24h / 7d / 30d with average and peak, stored at a
   fixed, bounded size.
3. Per-app disk usage (volumes + app directory + writable layers), refreshed
   hourly.
4. Org-wide alert rules with per-server / per-app overrides, delivered to
   org-wide Shoutrrr channels with a friendly form UI.
5. A dashboard control panel, a server monitoring page, an app Metrics tab
   with header info blocks, and an org Alerts page (History / Rules /
   Channels).

## Non-goals

- Realtime (1 s) view. 60 s samples are the finest resolution.
- GPU, battery, per-core CPU, per-interface network, SMART, ZFS.
- Configurable retention.
- Quiet hours, escalation, repeat-while-firing reminders.
- Bind-mount sizes outside the app directory.
- Rootless Docker container metrics (reported as unavailable).

## Delivery

One spec, two PRs:

- **PR 1 — metrics:** agent collection, storage/rollup/retention, chart API,
  dashboard control panel, server Monitoring page, app Metrics tab + header
  blocks.
- **PR 2 — alerts:** rules/overrides/states/events, evaluator, Shoutrrr
  channels and sender, org Alerts page, alert cards on server/app pages,
  alert badges.

## 1. Collection (agent)

### Loop

`RunMetricsReporter(ctx, sink, interval)` in `internal/app/agent`, same shape
as `RunStatusReporter` (ticker + `select ctx.Done()`), started next to it in
`cmd/agent/main.go` and `bootstrap/standalone.go`. Interval is fixed at 60 s.
Each tick emits one `server.metrics` event (new `bus.EventKind`
`EventServerMetrics`) through the existing `EventSink`. No proto change: the
hub forwards `AgentEvent` verbatim.

Disabled entirely when `METRICS_ENABLED=false`.

### Server sample

Read from procfs/sysfs directly (no gopsutil):

| Field | Source |
|---|---|
| `cpu` % | `/proc/stat` aggregate line, delta vs previous sample |
| `mem_used`, `mem_total` | `/proc/meminfo`: total − `MemAvailable` |
| `swap_used`, `swap_total` | `/proc/meminfo` |
| `load1/5/15` | `/proc/loadavg` |
| `disks[]` {mount, used, total} | statfs of `/` and of `AGENT_DATA_DIR`'s filesystem, deduped by `st_dev` |
| `disk_read_bps`, `disk_write_bps` | `/proc/diskstats`, whole physical disks only (skip partitions, `loop*`, `ram*`, `dm-*`, `zram*`) |
| `net_rx_bps`, `net_tx_bps` | `/proc/net/dev`, excluding `lo`, `veth*`, `docker*`, `br-*` |
| `temp` °C, `temps{}` | max over `/sys/class/hwmon/*/temp*_input`, label from `temp*_label` or hwmon `name`; absent when no sensors |
| `docker_df` {images, volumes, build_cache, reclaimable} | last hourly disk scan (see below) |

Rates are computed against the previous sample. The first sample after start
has no rates (fields omitted, not zero). A counter that goes backwards drops
that interval.

### App sample

Containers per app and their full IDs come from the `dockerPSByProject`
result the status reporter already fetches every 30 s: the metrics loop reads
a shared cached snapshot instead of running `docker ps` again.

Per container, from cgroupfs:

| Field | cgroup v2 | cgroup v1 fallback |
|---|---|---|
| CPU | `cpu.stat` `usage_usec`, delta → % of host | `cpuacct.usage` |
| Memory | `memory.current` − `inactive_file` from `memory.stat` | `memory.usage_in_bytes` − `total_inactive_file` |
| Limit | `memory.max` (`max` → none) | `memory.limit_in_bytes` (huge → none) |
| Net | `/proc/<first pid in cgroup.procs>/net/dev` (the container's netns), excluding `lo` | same |

cgroup path lookup tries `/sys/fs/cgroup/system.slice/docker-<id>.scope`
(systemd driver) then `/sys/fs/cgroup/docker/<id>` (cgroupfs driver), then the
v1 hierarchies. Containers sharing the host network namespace report no net.
If no layout resolves, the sample carries `containers_unavailable: true` and
the UI says so. There is no `docker stats` fallback.

App fields are the sums over its containers: `cpu`, `mem_used`, `mem_limit`
(sum of limits only when every container has one), `net_rx_bps`,
`net_tx_bps`, plus `disk` from the hourly scan.

Verified on the dev host (cgroup v2, systemd driver) as a non-root user:
`cpu.stat`, `memory.current`, `memory.max`, `cgroup.procs` and
`/proc/<pid>/net/dev` of a container are all readable.

Per-container delta state is pruned for IDs absent from the latest snapshot.

### Disk scan (hourly)

Volume data under `/var/lib/docker/volumes` is root-only; the agent is not
root. The daemon measures it:

- One `docker system df -v --format json` exec every `METRICS_DISK_INTERVAL`
  (default `1h`, `0` disables), with a 5-minute timeout. Measured at 43 s on
  the dev host: this is why it is hourly and off the 60 s path.
- Sizes arrive as human strings (`26.59MB`); a parser converts them to bytes.
- A volume belongs to an app when its `com.docker.compose.project` label is
  `wf-<appID>`, or else when it is mounted by exactly one app's containers
  (pre-created external volumes from the volume registry).
- Container `Size` (writable layer) is summed per app.
- `filepath.WalkDir` of `apps-data/{appID}` (config history, `source/`, bind
  mounts under it) gives the app directory size.
- App `disk` = volumes + app dir + writable layers. Image size per app is
  reported separately as `image` and not added (images can be shared).
- Host totals (images, volumes, build cache, reclaimable) go to the server
  sample's `docker_df`.

The last scan result is attached to every 60 s sample, so disk gets the same
rollups and 30-day charts as everything else.

## 2. Storage, rollup, chart API (API)

### Table

Migration `NNNN_metrics.sql` (next free number at implementation time: `0010`
on current main, `0011` if PR #12 lands first), identical in sqlite and pgsql:

```
metric_samples(
  server_id    TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  app_id       TEXT NOT NULL DEFAULT '',   -- '' = server row
  tier         INTEGER NOT NULL,           -- 1, 10, 20, 120, 480 (minutes)
  bucket_start TIMESTAMP NOT NULL,         -- UTC, aligned to tier
  samples      INTEGER NOT NULL,
  data         TEXT NOT NULL,              -- JSON {field: avg, field_max: max}
  PRIMARY KEY (server_id, app_id, tier, bucket_start)
)
```

Rows for a deleted app are removed by the app-delete path (same transaction
that deletes the app row). All inserts are insert-or-ignore on the primary
key, which makes them idempotent and safe with several API replicas.

### Write path

The `eventSink` gets a `server.metrics` case. One transaction writes the
server row and one row per app at tier 1, `bucket_start` = sample time
truncated to the minute. The event is then handed to the alert evaluator
(PR 2). The latest server sample is also kept in memory (pruned on server
delete) for dashboard meters.

### Rollup

A loop every 10 minutes (ticker + `ctx.Done()`, wired in `bootstrap/core.go`):
for each tier pair (1→10, 10→20, 20→120, 120→480), for each closed bucket
that has source rows and no destination row, write the destination row.
Every numeric field gets `avg` (weighted by `samples`) and `max` (max of the
inputs' `max`, or of the values at tier 1). Done in Go, so both engines
behave the same.

### Retention

Hourly delete per tier: 1 → 1 h, 10 → 12 h, 20 → 24 h, 120 → 7 d, 480 → 30 d.
About 380 rows per target; a server with 20 apps holds ~8k rows.

### Chart API

- `GET /api/v1/metrics/server?server_id=&range=1h|12h|24h|7d|30d`
- `GET /api/v1/metrics/app?server_id=&app_id=&range=…`
- `GET /api/v1/metrics/overview` — latest sample + 1 h CPU/mem/disk sparkline
  per server of the caller's org (dashboard).

Range → tier: 1h → 1, 12h → 10, 24h → 20, 7d → 120, 30d → 480. Response:
`{interval_seconds, points: [{t, …avg fields, …_max fields}]}`;
`interval_seconds` lets the UI break lines over gaps.

Authorization: `webutil.RequireServerAccess` on server-addressed routes (403
asserted per route); overview filters by org. OpenAPI regenerated, contract
tests + probes added.

## 3. Alerts (API, PR 2)

### Tables (migration `NNNN_alerts.sql`)

- `notification_channels(id, org_id FK cascade, name, url_encrypted, is_default, enabled, last_error, last_sent_at, created_at)`.
  The Shoutrrr URL contains secrets: it is encrypted at rest (AES-256-GCM,
  key derived with HKDF from `JWT_SECRET`, so rotating `JWT_SECRET` means
  re-entering channel URLs — documented in `.env.example`) and never
  returned to the browser; the API returns the service type and the
  non-secret fields only, and edits re-send the secret parts or keep the
  stored ones (same `<encrypted>` convention as app secrets).
- `alert_rules(id, org_id FK cascade, target_kind 'server'|'app', metric, threshold, duration_minutes 1..60, channel_ids JSON NULL, enabled, created_at)`.
  `channel_ids` NULL = the org's default channels. Several rules may share a
  metric (warn 80, crit 95).
- `alert_overrides(rule_id FK cascade, target_id, disabled, threshold NULL, duration_minutes NULL, channel_ids JSON NULL, PRIMARY KEY(rule_id, target_id))`.
- `alert_states(rule_id FK cascade, target_id, state 'ok'|'firing', since, last_value, PRIMARY KEY(rule_id, target_id))`.
- `alert_events(id, org_id, rule_id, server_id, app_id, kind 'fired'|'resolved', value, threshold, message, deliveries JSON, created_at)`.
  `deliveries` = per-channel `{channel_id, ok, error}`. Pruned to 90 days or
  2,000 rows per org, whichever is smaller.

Overrides, states and events are removed when their server/app is deleted.

### Metrics

| Target | Metric | Unit |
|---|---|---|
| server | `down` | — |
| server | `cpu`, `memory`, `disk` (fullest filesystem) | % |
| server | `load1`, `load5`, `load15` | load |
| server | `bandwidth` (rx + tx) | MB/s |
| server | `temperature` (hottest sensor; hidden when absent) | °C |
| app | `down` | — |
| app | `cpu` | % of host |
| app | `memory` | % of limit, else % of host |
| app | `disk` | GB |

### Evaluation

Runs only in the API instance that consumes events (already the sole
consumer per region). Effective settings = rule merged with the target's
override; disabled rules/overrides are skipped.

**Threshold metrics.** The evaluator holds the last 60 tier-1 values per
target in memory (refilled from tier-1 rows at startup, pruned on
server/app delete). On each sample it averages the values inside the
effective window; the result counts only when ≥ 80 % of the expected samples
exist. Above threshold → `firing`; at or below → `ok`. Each transition writes
`alert_states` (upsert), appends an `alert_events` row and queues one
notification. No repeats while firing.

**Server down.** Evaluated in the existing 15 s sweep from `last_seen`:
fires when offline ≥ duration, resolves on the next heartbeat. While a server
is down its app rules are not evaluated (no storm).

**App down.** Fires when the app's aggregated status has been
`problematic` (crashed, restart-looping, unhealthy) for ≥ duration, from
`apps.status` transitions. A user-stopped app is `stopped` and never fires.

### Delivery

Shoutrrr (`github.com/nicholas-fedor/shoutrrr`, MIT). A single sender
goroutine drains a bounded queue (drop + log when full), 10 s timeout per
channel send. Result recorded in `alert_events.deliveries` and on the
channel (`last_error`, `last_sent_at`). No retries.

Message: title `🔥 prod-1 · CPU 93% > 90% for 10m` /
`✅ Resolved · prod-1 · CPU 41%`, body with server/app names and a link built
from `WEB_URL` to the server Monitoring page or app Metrics tab.

### Defaults

Every org gets, once: server down 5 min, CPU > 90 % 10 min, memory > 90 %
10 min, disk > 90 % 5 min, app down 5 min — enabled, no channel restriction.
Seeding happens in Go at org creation and, for existing orgs, at API startup
for orgs whose new `organizations.alert_defaults_seeded` flag is false (the
flag is then set, so deleting the defaults is permanent). Without channels
they only appear in History.

### API

All under `/api/v1/alerts/`, org-scoped via the caller's org; target ids
checked for org ownership. Channels: list, create, update, delete, test
(`POST …/channels/test` with a draft or saved channel). Rules: list, create,
update, delete. Overrides: get/put/delete per (rule, target). Events: list
with filters (server, app, rule, kind, from/to) and cursor pagination.
Firing count per server/app is included in the metrics overview and app
responses for badges. New SSE type `alert` (fired/resolved) with an
`api.EventPayload` entry.

## 4. UI

Charts use the vendored shadcn `chart.tsx` over recharts 2.15 (already in
`package.json`).

- **Dashboard control panel** (`/`): the server cards become health widgets —
  online/offline + since, CPU/memory/disk meters with 1 h sparklines, load,
  temperature, app counts (running / stopped / problematic), firing-alert
  badge. An "Active alerts" strip above (PR 2). Click → Monitoring.
- **Server Monitoring** (new sidebar entry, `/monitoring`, for the switcher's
  active server): range select + avg/peak toggle; charts for CPU, memory +
  swap, load, disk usage, disk I/O, network, temperature; stacked area charts
  of CPU and memory per app; Docker disk breakdown; Alerts card with
  applicable rules and this server's overrides (PR 2).
- **App page** (`/app/:appId`): header info blocks — CPU now, memory vs
  limit, disk, network rate, firing-alert badge (PR 2). New **Metrics** tab:
  CPU, memory, network, disk-growth charts with the same range select; Alerts
  card with this app's overrides (PR 2).
- **Org → Alerts** (`/org/alerts`, linked next to Members): tabs **History**
  (newest first, filters, links to target, delivery status), **Rules**
  (CRUD, enable toggle), **Channels** (list with default flag, last error,
  Test; create/edit dialog with a service picker — Telegram, Discord, Slack,
  ntfy, Gotify, Pushover, email/SMTP, generic webhook — rendering labelled
  fields that build the Shoutrrr URL, plus a "raw URL" option).

Empty states: "No data yet" for the first minutes; "Container metrics are
unavailable on this host" when cgroups do not resolve; "No temperature
sensors" hides the chart.

## 5. Config

Added to `.env.example`:

- `METRICS_ENABLED` (default `true`) — agent and standalone: collect and
  send; API: store. All topologies.
- `METRICS_DISK_INTERVAL` (default `1h`, `0` disables) — agent and
  standalone: hourly `docker system df -v` + app dir walk.

## 6. Resource and safety checks

- 60 s path: file reads only (~6 host + ~3 per container). The only exec is
  the hourly `docker system df -v`, bounded by a 5-minute timeout.
- No per-tick SSE fan-out of metrics; the UI fetches charts on demand and
  refreshes every 60 s while visible.
- Bounded storage (tiered retention), bounded history (90 d / 2,000), bounded
  in-memory buffers (60 values per target, pruned on delete), bounded send
  queue.
- Every network send has a 10 s deadline; the standalone `InProcessBridge`
  is not involved (metrics are events, not commands).
- Channel URLs encrypted at rest and never returned in full.
- All server/app-addressed routes go through `RequireServerAccess` / org
  checks with 403 contract tests.

## 7. Testing

- Agent: parsers for `/proc/stat`, `meminfo`, `loadavg`, `diskstats`,
  `net/dev`, hwmon, cgroup v1/v2 files and `docker system df` JSON against
  fixture trees (fs root injectable); delta/first-sample/counter-reset cases.
- API: rollup math (weighted avg, max-of-max, idempotency), retention,
  range→tier mapping, repository tests on both engines (`make test-pg`).
- Evaluator: window averaging, 80 % coverage rule, fire/resolve transitions,
  override merge, down suppression, restart-from-state.
- Sender: fake Shoutrrr sender; timeout and queue-full behaviour.
- Contract: all new routes, probes, 403 per server-addressed route; OpenAPI
  regenerated.
- Web: component tests for chart cards, channel form → URL builder, rules
  and overrides forms.
