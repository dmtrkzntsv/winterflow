# Process list on the Monitoring page — design

Date: 2026-10-14. Status: approved in chat, awaiting spec review.

## Goal

A **Processes** tab on Monitoring that shows what is running on the active
server and what each process costs (CPU, memory, disk reads and writes), so
the user can find what is loading the box. The list sorts and filters. It is
a live view like `top`: nothing is stored, there is no history, and **data is
pulled only while the tab is open and the page is visible**.

## Decisions

- On-demand agent command polled by the tab. Rejected: riding the 60 s
  `server.metrics` event (too coarse, always on, would persist process data)
  and an SSE push subscription (subscribe bookkeeping across API replicas for
  no gain over polling).
- The result travels the normal command path: the route answers **202 +
  `request_id`**, the list arrives over SSE as the command's result
  notification (published to the dispatching user only), and the browser
  reads it with `useNotifications().waitFor(ref)`, exactly like `app.logs`.
- The installed unit gains `CAP_SYS_PTRACE` so `/proc/<pid>/io` is readable
  for every process. Where it still is not (an old unit, a dev run as a
  normal user), the disk columns are `null` and render "—".
- Read-only: no kill/renice actions.

## Backend

### Command

`internal/domain/command`: `TypeProcessList Type = "process.list"`, not
detached. Request `ProcessListRequest{}` (empty). Response:

```go
type ProcessListResponse struct {
    SampledAt int64     `json:"sampled_at"` // unix ms
    NCPU      int       `json:"ncpu"`
    MemTotal  uint64    `json:"mem_total"`  // bytes
    Processes []Process `json:"processes"`
}

type Process struct {
    PID         int      `json:"pid"`
    PPID        int      `json:"ppid"`
    Name        string   `json:"name"`         // /proc/<pid>/stat comm
    Cmdline     string   `json:"cmdline"`      // NUL→space, capped at 512 B
    User        string   `json:"user"`         // name from /etc/passwd, else the uid
    State       string   `json:"state"`        // R, S, D, Z, T, I …
    Threads     int      `json:"threads"`
    StartTime   int64    `json:"start_time"`   // unix s
    Kernel      bool     `json:"kernel"`       // kernel thread (ppid 2 or pid 2)
    CPUPct      *float64 `json:"cpu_pct"`      // per core (top-style, may exceed 100); nil on first sight
    RSS         uint64   `json:"rss"`          // bytes
    MemPct      float64  `json:"mem_pct"`      // RSS / MemTotal
    ReadBps     *float64 `json:"read_bps"`     // nil when io unreadable or first sight
    WriteBps    *float64 `json:"write_bps"`
    ContainerID string   `json:"container_id,omitempty"`
    AppID       string   `json:"app_id,omitempty"`
}
```

### Reader — `internal/infra/sysmetrics/processes.go`

- `type Processes struct` built with `NewProcesses(root string, ncpu int)`,
  paths resolved under the injectable root like the other readers.
- `Collect(now time.Time) ([]Process, error)`: list numeric entries of
  `/proc`; per pid read `stat` (comm, state, ppid, utime+stime, num_threads,
  starttime), `status` (Uid, VmRSS), `cmdline`, `io` (`read_bytes`,
  `write_bytes`; a permission error just leaves the rates nil) and `cgroup`
  (container id: the 64-hex docker id in a cgroup v2 `docker-<id>.scope` or
  v1 `/docker/<id>` path). A pid that vanishes mid-read is skipped, never an
  error. File reads only; no forks.
- Rates: the reader keeps the previous reading per pid (cpu ticks, io bytes,
  time). CPU % = Δticks / USER_HZ / Δt × 100. When there is no previous
  reading, or it is older than 60 s, `Collect` takes a second reading ~1 s
  later so the first answer already has rates; afterwards a poll every 5 s
  uses the previous call. Pids not seen in the latest reading are dropped;
  the whole map is dropped when it is older than 60 s, so the idle cost when
  nobody watches is zero.
- Boot time (`btime` in `/proc/stat`) converts starttime ticks to unix
  seconds. User names come from `/etc/passwd` read once and reread when its
  mtime changes.
- Concurrency: one mutex around `Collect` (two browsers polling at once take
  turns; each call is cheap).

### Agent

- One `newHandlers` entry in `internal/app/agent/dispatcher.go` for
  `TypeProcessList`, with a 10 s deadline on the call.
- Container → app: match `ContainerID` against the status reporter's
  `StatusSnapshot` (same source as the metrics loop; no `docker ps`).
- Off Linux (`statfs_other.go` style build tags) the handler answers an
  error "process list is only available on Linux".

### API

- Usecase: `GetProcesses(ctx, userID, serverID) (requestID string, err error)`
  dispatching `TypeProcessList`, beside `GetLogs`.
- Route `GET /api/v1/server/get-processes?server_id=…` in the server
  handler's `Routes` table: `RequireServerAccess`, then dispatch, then
  `202 + request_id` (same `accepted` shape as get-logs). The result payload
  is `command.ProcessListResponse`; document it as the route's result
  schema in the route table so `make openapi` covers it.
- Contract test entry (happy path + probe), handler test asserting 403 for a
  server the caller's org does not own.

### Installer

`scripts/install.sh` unit: `AmbientCapabilities=CAP_NET_BIND_SERVICE
CAP_SYS_PTRACE` and the same in `CapabilityBoundingSet`, with a comment
(per-process disk I/O on the Monitoring page; the agent already holds the
Docker socket, which is root-equivalent). Update `scripts/install_test.sh`.
Existing installs get it on the next installer run.

## Web — Processes tab

- `web/src/pages/monitoring.tsx` gets `UnderlineTabs` `Overview | Processes`
  (as in app details). The current page body is Overview, unchanged. The
  tab is in the URL (`?tab=processes`).
- `web/src/components/monitoring/processes-tab.tsx` mounts only while
  selected. Hook `useProcesses(serverId, enabled)` where
  `enabled = tab selected && document visible && !paused`:
  a single sequential loop — GET get-processes → `waitFor(ref, 15000)` →
  set data → sleep 5 s → repeat. Disabling, unmounting or a server change
  aborts the loop (an `AbortController` for the fetch, a cancelled flag for
  the wait and the sleep). A visibilitychange to visible restarts it at
  once.
- Header: `DataFreshness` (fetched N s ago), pause/resume, "N cores" note.
- Table (columns): Process (name, truncated command below, full on hover) ·
  App (badge linking to the app; blank for host processes) · User · PID ·
  CPU % · Memory (RSS + % of host) · Read/s · Write/s ("—" when null) ·
  Threads · Uptime.
- Sort: click a header; default CPU descending; the chosen sort survives
  every refresh; ties break by PID so rows do not jitter.
- Filter (client-side): a text box matching name, command, user, PID and app
  name; an App select (All / one app / Host only); "Show kernel threads"
  toggle, off by default.
- Mobile: the table shows Process · CPU · Memory · Write; the other fields
  sit in a row that expands on tap.
- States: skeleton rows on first load; an error inline (agent offline,
  timeout) while the loop keeps retrying; "No processes match." for an empty
  filter.

## Testing

- Go: fixture-tree tests for the reader (two readings → CPU % and rates; a
  pid vanishing between readings; unreadable `io` → nil rates; cgroup v1 and
  v2 container ids; kernel threads; cmdline cap; stale map dropped after
  60 s). Dispatcher test for the handler; handler test for 202 and 403;
  contract entry; `make openapi`.
- Web (vitest): polling runs only while the tab is selected and the page is
  visible and stops on tab switch/hide/pause; sort by a column and its
  persistence across a refresh; text filter, app filter, kernel toggle; "—"
  for null rates.
- `bash scripts/install_test.sh` after the unit change.

## Out of scope

Process actions (kill, renice), a process tree view, history or charts per
process, alerts on processes, Windows/macOS agents.
