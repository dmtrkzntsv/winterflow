# Process List Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Processes tab on the Monitoring page listing the active server's processes (CPU, memory, disk read/write, app) with sorting and filtering, polled only while the tab is open and the page visible.

**Architecture:** A new agent command `process.list` reads `/proc` (file reads only, no forks) through a `sysmetrics.Processes` reader that keeps the previous reading for rates. The agent maps container ids to apps through the status reporter's `StatusSnapshot`. The API route `GET /api/v1/server/get-processes` (docker handler, which already hosts server-addressed agent list commands) answers 202 + `request_id`; the list arrives over SSE as the command result and the browser reads it with `useNotifications().waitFor(ref)`, exactly like `app.logs`. The installer grants `CAP_SYS_PTRACE` so `/proc/<pid>/io` is readable for every process.

**Tech Stack:** Go 1.27 (stdlib only), React 19 + Vite + vitest + Testing Library, shadcn/ui (vendored), Tailwind.

**Spec:** `docs/superpowers/specs/2026-10-14-process-list-design.md`

## Global Constraints

- Agent reads are file reads only — never exec `ps`/`docker`/anything in this path (CLAUDE.md "Never exec per app in polled paths").
- Every path under the injectable root (`"/"` in production) so tests run on fixture trees (`internal/infra/sysmetrics/fixture_test.go` `tree`/`write`).
- `cmdline` capped at 512 bytes; NUL separators become spaces; trailing space trimmed.
- An answer carries at most `command.MaxProcesses = 2000` processes (the busiest first: CPU desc, then RSS desc, then PID asc) and `total` = processes seen, so a huge host never approaches gRPC's 4 MB default message limit.
- CPU % is per core (top-style; may exceed 100). `USER_HZ` = 100.
- Previous readings older than 60 s are ignored (a fresh call takes two readings 1 s apart).
- Agent handler deadline 10 s. Browser waits 15 s per reply, sleeps 5 s between replies, one request in flight at a time.
- Route answers 202 + `{request_id}`; ownership via the docker handler's `caller` (403 on a foreign server, nothing dispatched).
- Data pulled only while the Processes tab is selected AND `document.visibilityState === "visible"` AND not paused.
- Never delete files from `web/src/components/ui/` (vendored shadcn).
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **PID reuse between readings** — a new process that got an old pid must not inherit the old one's counters (CPU % would be huge or negative). Rates are computed only when the previous reading has the same `starttime`; Task 1 test `TestProcessesPIDReuse`.
2. **A process name containing spaces or `)`** (e.g. `(sd-pam)`, `tmux: server`, `a) b`) — `/proc/<pid>/stat` must be parsed from the LAST `)`; Task 1 test `TestParsePIDStatOddComm`.
3. **A server change or tab switch while a request is in flight** — the late reply must not land on the new server's table, and the loop must not keep polling; Task 5 tests `stops polling when disabled` and `drops a late reply after the server changed`.
4. **The agent offline / never answering** — `waitFor` times out after 15 s; the tab shows an inline error, keeps the last good data, and retries; Task 5 test `shows a timeout and keeps polling`.
5. **Counters going backwards or `io` unreadable** (permission denied without `CAP_SYS_PTRACE`) — rates are `null` ("—" in the UI), never negative or an error; Task 1 test `TestProcessesUnreadableIO`, Task 6 test `renders a dash for unknown rates`.

---

### Task 1: Command types and the `/proc` reader

**Files:**
- Create: `internal/domain/command/process.go`
- Modify: `internal/domain/command/command.go` (const block, before `// Agent self-management.`)
- Create: `internal/infra/sysmetrics/processes.go`
- Test: `internal/infra/sysmetrics/processes_test.go`

**Interfaces:**
- Produces: `command.TypeProcessList`, `command.ProcessListRequest`, `command.ProcessListResponse`, `command.Process`, `command.MaxProcesses`; `sysmetrics.NewProcesses(root string, ncpu int) *Processes`; `(*Processes).List() (command.ProcessListResponse, error)` (AppID left empty — the agent fills it).

- [ ] **Step 1: Add the command types**

`internal/domain/command/command.go`, inside the const block before `// Agent self-management.`:

```go
	// Host processes (Monitoring's Processes tab).
	TypeProcessList Type = "process.list" // read every process's CPU, memory and disk I/O
```

`internal/domain/command/process.go`:

```go
package command

// MaxProcesses bounds a process.list answer: the busiest processes are
// kept (CPU, then memory), so a host with thousands of processes stays far
// below the gRPC message limit. Total says how many there were.
const MaxProcesses = 2000

// ProcessListRequest asks the agent for its host's processes.
type ProcessListRequest struct{}

// ProcessListResponse is one reading of the host's processes.
type ProcessListResponse struct {
	SampledAt int64     `json:"sampled_at"` // unix ms
	NCPU      int       `json:"ncpu"`
	MemTotal  uint64    `json:"mem_total"` // bytes
	Total     int       `json:"total"`     // processes seen; len(Processes) is at most MaxProcesses
	Processes []Process `json:"processes"`
}

// Process is one process. The rates are nil when unknown: the process
// started between the two readings, or /proc/<pid>/io is not readable to
// the agent (it needs CAP_SYS_PTRACE for other users' processes).
type Process struct {
	PID         int      `json:"pid"`
	PPID        int      `json:"ppid"`
	Name        string   `json:"name"`
	Cmdline     string   `json:"cmdline"`
	User        string   `json:"user"`
	State       string   `json:"state"`
	Threads     int      `json:"threads"`
	StartTime   int64    `json:"start_time"` // unix s
	Kernel      bool     `json:"kernel"`
	CPUPct      *float64 `json:"cpu_pct"` // per core, top-style: may exceed 100
	RSS         uint64   `json:"rss"`     // bytes
	MemPct      float64  `json:"mem_pct"`
	ReadBps     *float64 `json:"read_bps"`
	WriteBps    *float64 `json:"write_bps"`
	ContainerID string   `json:"container_id,omitempty"`
	AppID       string   `json:"app_id,omitempty"`
}
```

- [ ] **Step 2: Write the failing tests**

`internal/infra/sysmetrics/processes_test.go`:

```go
package sysmetrics

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
)

// statLine renders /proc/<pid>/stat with the fields the reader uses.
func statLine(pid int, comm, state string, ppid int, utime, stime uint64, threads int, start uint64) string {
	return fmt.Sprintf("%d (%s) %s %d 0 0 0 0 0 0 0 0 0 %d %d 0 0 20 0 %d 0 %d 0 0\n",
		pid, comm, state, ppid, utime, stime, threads, start)
}

func ioFile(read, write uint64) string {
	return fmt.Sprintf("rchar: 1\nwchar: 1\nsyscr: 1\nsyscw: 1\nread_bytes: %d\nwrite_bytes: %d\ncancelled_write_bytes: 0\n", read, write)
}

const cid = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func baseTree(t *testing.T) string {
	return tree(t, map[string]string{
		"proc/stat":    "cpu  1 2 3 4 5 6 7 8\nbtime 1700000000\n",
		"proc/meminfo": "MemTotal: 1000 kB\nMemAvailable: 500 kB\n",
		"etc/passwd":   "root:x:0:0:root:/root:/bin/sh\nalice:x:1000:1000::/home/alice:/bin/sh\n",
		// systemd-like root process
		"proc/1/stat":    statLine(1, "systemd", "S", 0, 100, 0, 1, 500),
		"proc/1/status":  "Name:\tsystemd\nUid:\t0\t0\t0\t0\nVmRSS:\t   100 kB\n",
		"proc/1/cmdline": "/sbin/init\x00splash\x00",
		"proc/1/io":      ioFile(1000, 2000),
		"proc/1/cgroup":  "0::/init.scope\n",
		// kthreadd and a kernel worker
		"proc/2/stat": statLine(2, "kthreadd", "S", 0, 0, 0, 1, 10),
		"proc/3/stat": statLine(3, "kworker/0:1", "I", 2, 0, 0, 1, 11),
		// a container process owned by alice
		"proc/42/stat":    statLine(42, "nginx: worker", "R", 1, 10, 10, 4, 900),
		"proc/42/status":  "Name:\tnginx\nUid:\t1000\t1000\t1000\t1000\nVmRSS:\t   250 kB\n",
		"proc/42/cmdline": "nginx: worker process\x00",
		"proc/42/io":      ioFile(0, 0),
		"proc/42/cgroup":  "0::/system.slice/docker-" + cid + ".scope\n",
		"proc/self/":      "", // non-numeric entries are skipped
	})
}

// fakeClock drives List: sleep advances the clock and runs between, which
// rewrites the fixture as the second reading should see it.
type fakeClock struct {
	t       time.Time
	between func()
	slept   int
}

func (c *fakeClock) now() time.Time { return c.t }
func (c *fakeClock) sleep(d time.Duration) {
	c.slept++
	c.t = c.t.Add(d)
	if c.between != nil {
		c.between()
	}
}

func newTestProcesses(root string, c *fakeClock) *Processes {
	p := NewProcesses(root, 4)
	p.now, p.sleep = c.now, c.sleep
	return p
}

func byPID(list []command.Process) map[int]command.Process {
	m := map[int]command.Process{}
	for _, p := range list {
		m[p.PID] = p
	}
	return m
}

func TestProcessesFirstCallTakesTwoReadings(t *testing.T) {
	root := baseTree(t)
	c := &fakeClock{t: time.Unix(1700001000, 0)}
	c.between = func() {
		write(t, root, map[string]string{
			"proc/1/stat":  statLine(1, "systemd", "S", 0, 150, 0, 1, 500), // +50 ticks in 1 s = 50 %
			"proc/1/io":    ioFile(1000, 6000),                             // +4000 B/s written
			"proc/42/stat": statLine(42, "nginx: worker", "R", 1, 110, 110, 4, 900),
		})
	}
	resp, err := newTestProcesses(root, c).List()
	if err != nil {
		t.Fatal(err)
	}
	if c.slept != 1 {
		t.Fatalf("slept %d times, want 1", c.slept)
	}
	if resp.NCPU != 4 || resp.MemTotal != 1000*1024 || resp.Total != 4 || resp.SampledAt != c.t.UnixMilli() {
		t.Fatalf("header = %+v", resp)
	}
	got := byPID(resp.Processes)
	init := got[1]
	if init.CPUPct == nil || *init.CPUPct != 50 {
		t.Fatalf("pid 1 cpu = %v", init.CPUPct)
	}
	if init.WriteBps == nil || *init.WriteBps != 4000 || init.ReadBps == nil || *init.ReadBps != 0 {
		t.Fatalf("pid 1 io = %v/%v", init.ReadBps, init.WriteBps)
	}
	if init.User != "root" || init.Cmdline != "/sbin/init splash" || init.RSS != 100*1024 || init.MemPct != 10 {
		t.Fatalf("pid 1 = %+v", init)
	}
	if init.StartTime != 1700000000+5 || init.Kernel || init.ContainerID != "" {
		t.Fatalf("pid 1 start/kernel/container = %+v", init)
	}
	nginx := got[42]
	if nginx.CPUPct == nil || *nginx.CPUPct != 200 { // 200 ticks in 1 s: two cores
		t.Fatalf("pid 42 cpu = %v", nginx.CPUPct)
	}
	if nginx.Name != "nginx: worker" || nginx.User != "alice" || nginx.Threads != 4 || nginx.State != "R" || nginx.PPID != 1 {
		t.Fatalf("pid 42 = %+v", nginx)
	}
	if nginx.ContainerID != cid {
		t.Fatalf("pid 42 container = %q", nginx.ContainerID)
	}
	if !got[2].Kernel || !got[3].Kernel {
		t.Fatalf("kernel threads = %+v %+v", got[2], got[3])
	}
	// Busiest first.
	if resp.Processes[0].PID != 42 || resp.Processes[1].PID != 1 {
		t.Fatalf("order = %v", []int{resp.Processes[0].PID, resp.Processes[1].PID})
	}
}

func TestProcessesLaterCallUsesPreviousReading(t *testing.T) {
	root := baseTree(t)
	c := &fakeClock{t: time.Unix(1700001000, 0)}
	p := newTestProcesses(root, c)
	if _, err := p.List(); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(5 * time.Second)
	write(t, root, map[string]string{"proc/1/stat": statLine(1, "systemd", "S", 0, 200, 0, 1, 500)})
	resp, err := p.List()
	if err != nil {
		t.Fatal(err)
	}
	if c.slept != 1 {
		t.Fatalf("second call slept: %d", c.slept)
	}
	if cpu := byPID(resp.Processes)[1].CPUPct; cpu == nil || *cpu != 20 { // 100 ticks / 5 s
		t.Fatalf("cpu = %v", cpu)
	}
}

func TestProcessesStalePreviousReadingIsIgnored(t *testing.T) {
	root := baseTree(t)
	c := &fakeClock{t: time.Unix(1700001000, 0)}
	p := newTestProcesses(root, c)
	if _, err := p.List(); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(61 * time.Second)
	if _, err := p.List(); err != nil {
		t.Fatal(err)
	}
	if c.slept != 2 {
		t.Fatalf("a call after 61 s must take two readings, slept %d", c.slept)
	}
}

func TestProcessesPIDReuse(t *testing.T) {
	root := baseTree(t)
	c := &fakeClock{t: time.Unix(1700001000, 0)}
	c.between = func() {
		// pid 42 exited and a new process got its pid (different starttime).
		write(t, root, map[string]string{"proc/42/stat": statLine(42, "sh", "S", 1, 1, 1, 1, 950)})
	}
	resp, err := newTestProcesses(root, c).List()
	if err != nil {
		t.Fatal(err)
	}
	if p := byPID(resp.Processes)[42]; p.CPUPct != nil || p.WriteBps != nil {
		t.Fatalf("reused pid inherited rates: %+v", p)
	}
}

func TestProcessesVanishedPID(t *testing.T) {
	root := baseTree(t)
	c := &fakeClock{t: time.Unix(1700001000, 0)}
	c.between = func() { removeAll(t, root, "proc/42") }
	resp, err := newTestProcesses(root, c).List()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := byPID(resp.Processes)[42]; ok || resp.Total != 3 {
		t.Fatalf("vanished pid listed: total %d", resp.Total)
	}
}

func TestProcessesUnreadableIO(t *testing.T) {
	root := baseTree(t)
	removeAll(t, root, "proc/42/io") // stands in for EACCES: the reader treats both alike
	c := &fakeClock{t: time.Unix(1700001000, 0)}
	resp, err := newTestProcesses(root, c).List()
	if err != nil {
		t.Fatal(err)
	}
	p := byPID(resp.Processes)[42]
	if p.ReadBps != nil || p.WriteBps != nil || p.CPUPct == nil {
		t.Fatalf("pid 42 = %+v", p)
	}
}

func TestProcessesCountersBackwards(t *testing.T) {
	root := baseTree(t)
	c := &fakeClock{t: time.Unix(1700001000, 0)}
	c.between = func() { write(t, root, map[string]string{"proc/1/io": ioFile(10, 10)}) }
	resp, err := newTestProcesses(root, c).List()
	if err != nil {
		t.Fatal(err)
	}
	if p := byPID(resp.Processes)[1]; p.ReadBps != nil || p.WriteBps != nil {
		t.Fatalf("negative rate not dropped: %+v", p)
	}
}

func TestProcessesCapAndCmdline(t *testing.T) {
	files := map[string]string{
		"proc/stat":    "cpu  1 2 3 4 5 6 7 8\nbtime 1700000000\n",
		"proc/meminfo": "MemTotal: 1000 kB\n",
	}
	for pid := 10; pid < 10+command.MaxProcesses+5; pid++ {
		files[fmt.Sprintf("proc/%d/stat", pid)] = statLine(pid, "p", "S", 1, uint64(pid), 0, 1, 1)
	}
	files["proc/10/cmdline"] = strings.Repeat("x", 2000)
	root := tree(t, files)
	resp, err := newTestProcesses(root, &fakeClock{t: time.Unix(1700001000, 0)}).List()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != command.MaxProcesses+5 || len(resp.Processes) != command.MaxProcesses {
		t.Fatalf("total %d, listed %d", resp.Total, len(resp.Processes))
	}
	for _, p := range resp.Processes {
		if p.PID == 10 && len(p.Cmdline) != 512 {
			t.Fatalf("cmdline len %d", len(p.Cmdline))
		}
	}
}

func TestParsePIDStatOddComm(t *testing.T) {
	s, err := parsePIDStat([]byte(statLine(7, "a) b", "S", 3, 5, 6, 2, 99)))
	if err != nil {
		t.Fatal(err)
	}
	if s.comm != "a) b" || s.state != "S" || s.ppid != 3 || s.ticks != 11 || s.threads != 2 || s.start != 99 {
		t.Fatalf("parsed %+v", s)
	}
	if _, err := parsePIDStat([]byte("7 (short) S 1\n")); err == nil {
		t.Fatal("a truncated stat must be an error")
	}
}

func TestContainerIDFromCgroup(t *testing.T) {
	for in, want := range map[string]string{
		"0::/system.slice/docker-" + cid + ".scope\n":           cid,
		"0::/docker/" + cid + "\n":                              cid,
		"12:memory:/docker/" + cid + "\n4:cpu:/docker/" + cid:   cid,
		"0::/user.slice/user-1000.slice/session-2.scope\n":      "",
		"0::/system.slice/containerd.service\n":                 "",
	} {
		if got := containerIDFromCgroup([]byte(in)); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
```

Add to `internal/infra/sysmetrics/fixture_test.go`:

```go
func removeAll(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(root, rel)); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/infra/sysmetrics/ -run 'TestProcesses|TestParsePIDStat|TestContainerID'`
Expected: FAIL — `undefined: NewProcesses`, `parsePIDStat`, `containerIDFromCgroup`.

- [ ] **Step 4: Implement the reader**

`internal/infra/sysmetrics/processes.go`:

```go
package sysmetrics

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"winterflow/internal/domain/command"
)

const (
	clkTck     = 100 // USER_HZ: 100 in every Linux architecture's userspace ABI
	cmdlineCap = 512
	// prevMaxAge: an older previous reading is not used for rates (nobody
	// watched for a while); the call takes two readings instead.
	prevMaxAge = 60 * time.Second
	// firstGap separates the two readings of a call without a usable
	// previous one, so the first answer already has rates.
	firstGap = time.Second
)

// Processes reads every process under root/proc. It keeps the previous
// reading per pid, so a call's CPU % and disk rates cover the time since
// the call before. File reads only.
type Processes struct {
	root  string
	ncpu  int
	now   func() time.Time
	sleep func(time.Duration)

	mu     sync.Mutex
	prev   map[int]procCounters
	prevAt time.Time
	users  map[int]string
	userAt time.Time // /etc/passwd mtime users was read at
}

// NewProcesses reads under root ("/" in production).
func NewProcesses(root string, ncpu int) *Processes {
	return &Processes{root: root, ncpu: ncpu, now: time.Now, sleep: time.Sleep}
}

// procCounters is what rates are computed from. start (starttime ticks)
// tells a reused pid apart.
type procCounters struct {
	start, ticks uint64
	read, write  uint64
	io           bool
}

type procReading struct {
	p command.Process
	c procCounters
}

func (p *Processes) path(parts ...string) string {
	return filepath.Join(append([]string{p.root}, parts...)...)
}

// List takes a reading and returns it with rates since the previous one.
// Calls take turns: the reader's state is shared.
func (p *Processes) List() (command.ProcessListResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadUsers()
	now := p.now()
	if p.prev == nil || now.Sub(p.prevAt) > prevMaxAge {
		first, err := p.read()
		if err != nil {
			return command.ProcessListResponse{}, err
		}
		p.remember(first, now)
		p.sleep(firstGap)
		now = p.now()
	}
	cur, err := p.read()
	if err != nil {
		return command.ProcessListResponse{}, err
	}
	var memTotal uint64
	if b, err := os.ReadFile(p.path("proc", "meminfo")); err == nil {
		if mi, err := parseMeminfo(b); err == nil {
			memTotal = mi.total
		}
	}
	dt := now.Sub(p.prevAt).Seconds()
	out := make([]command.Process, 0, len(cur))
	for pid, r := range cur {
		proc := r.p
		if prev, ok := p.prev[pid]; ok && prev.start == r.c.start && dt > 0 {
			if r.c.ticks >= prev.ticks {
				v := float64(r.c.ticks-prev.ticks) / clkTck / dt * 100
				proc.CPUPct = &v
			}
			if r.c.io && prev.io && r.c.read >= prev.read && r.c.write >= prev.write {
				rd := float64(r.c.read-prev.read) / dt
				wr := float64(r.c.write-prev.write) / dt
				proc.ReadBps, proc.WriteBps = &rd, &wr
			}
		}
		if memTotal > 0 {
			proc.MemPct = float64(proc.RSS) / float64(memTotal) * 100
		}
		out = append(out, proc)
	}
	p.remember(cur, now)
	sort.Slice(out, func(i, j int) bool {
		ci, cj := cpuOf(out[i]), cpuOf(out[j])
		if ci != cj {
			return ci > cj
		}
		if out[i].RSS != out[j].RSS {
			return out[i].RSS > out[j].RSS
		}
		return out[i].PID < out[j].PID
	})
	total := len(out)
	if len(out) > command.MaxProcesses {
		out = out[:command.MaxProcesses]
	}
	return command.ProcessListResponse{
		SampledAt: now.UnixMilli(), NCPU: p.ncpu, MemTotal: memTotal, Total: total, Processes: out,
	}, nil
}

func cpuOf(p command.Process) float64 {
	if p.CPUPct == nil {
		return -1
	}
	return *p.CPUPct
}

func (p *Processes) remember(r map[int]procReading, at time.Time) {
	p.prev = make(map[int]procCounters, len(r))
	for pid, x := range r {
		p.prev[pid] = x.c
	}
	p.prevAt = at
}

// read takes one reading of every process. A pid that vanishes while it is
// read is skipped.
func (p *Processes) read() (map[int]procReading, error) {
	stat, err := os.ReadFile(p.path("proc", "stat"))
	if err != nil {
		return nil, err
	}
	btime := parseBtime(stat)
	entries, err := os.ReadDir(p.path("proc"))
	if err != nil {
		return nil, err
	}
	out := make(map[int]procReading, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if r, ok := p.readPID(pid, btime); ok {
			out[pid] = r
		}
	}
	return out, nil
}

func (p *Processes) readPID(pid int, btime int64) (procReading, bool) {
	dir := strconv.Itoa(pid)
	b, err := os.ReadFile(p.path("proc", dir, "stat"))
	if err != nil {
		return procReading{}, false
	}
	s, err := parsePIDStat(b)
	if err != nil {
		return procReading{}, false
	}
	proc := command.Process{
		PID: pid, PPID: s.ppid, Name: s.comm, State: s.state, Threads: s.threads,
		StartTime: btime + int64(s.start/clkTck),
		Kernel:    pid == 2 || s.ppid == 2,
	}
	c := procCounters{start: s.start, ticks: s.ticks}
	if b, err := os.ReadFile(p.path("proc", dir, "status")); err == nil {
		uid, rss := parseStatus(b)
		proc.RSS = rss
		if uid >= 0 {
			proc.User = p.userName(uid)
		}
	}
	if b, err := os.ReadFile(p.path("proc", dir, "cmdline")); err == nil {
		proc.Cmdline = cleanCmdline(b)
	}
	if b, err := os.ReadFile(p.path("proc", dir, "io")); err == nil {
		c.read, c.write, c.io = parseIO(b)
	}
	if b, err := os.ReadFile(p.path("proc", dir, "cgroup")); err == nil {
		proc.ContainerID = containerIDFromCgroup(b)
	}
	return procReading{p: proc, c: c}, true
}

type pidStat struct {
	comm, state            string
	ppid, threads          int
	ticks, start           uint64
}

// parsePIDStat reads /proc/<pid>/stat. comm may hold spaces and ')', so
// the fields after it are found from the last ')'.
func parsePIDStat(b []byte) (pidStat, error) {
	open, end := bytes.IndexByte(b, '('), bytes.LastIndexByte(b, ')')
	if open < 0 || end < open {
		return pidStat{}, errors.New("stat: no comm")
	}
	rest := strings.Fields(string(b[end+1:]))
	// rest[0] is field 3 (state): field n is rest[n-3].
	if len(rest) < 20 {
		return pidStat{}, errors.New("stat: short")
	}
	num := func(i int) uint64 { v, _ := strconv.ParseUint(rest[i], 10, 64); return v }
	return pidStat{
		comm: string(b[open+1 : end]), state: rest[0],
		ppid: int(num(1)), ticks: num(11) + num(12), threads: int(num(17)), start: num(19),
	}, nil
}

// parseBtime reads the boot time (unix s) from /proc/stat; 0 when absent.
func parseBtime(b []byte) int64 {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "btime "); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n
		}
	}
	return 0
}

// parseStatus reads the real uid (-1 when absent) and VmRSS in bytes (0 for
// kernel threads, which have none).
func parseStatus(b []byte) (uid int, rss uint64) {
	uid = -1
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(val)
		if len(f) == 0 {
			continue
		}
		switch key {
		case "Uid":
			if v, err := strconv.Atoi(f[0]); err == nil {
				uid = v
			}
		case "VmRSS":
			v, _ := strconv.ParseUint(f[0], 10, 64)
			rss = v * 1024
		}
	}
	return uid, rss
}

// parseIO reads the bytes that reached storage (read_bytes, write_bytes).
func parseIO(b []byte) (read, write uint64, ok bool) {
	var seenR, seenW bool
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		key, val, found := strings.Cut(sc.Text(), ":")
		if !found {
			continue
		}
		v, err := strconv.ParseUint(strings.TrimSpace(val), 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "read_bytes":
			read, seenR = v, true
		case "write_bytes":
			write, seenW = v, true
		}
	}
	return read, write, seenR && seenW
}

func cleanCmdline(b []byte) string {
	if len(b) > cmdlineCap {
		b = b[:cmdlineCap]
	}
	return strings.TrimRight(strings.ReplaceAll(string(b), "\x00", " "), " ")
}

// dockerCgroup matches a Docker container's cgroup: systemd driver
// (docker-<id>.scope) or cgroupfs driver (/docker/<id>), v1 or v2.
var dockerCgroup = regexp.MustCompile(`docker[-/]([0-9a-f]{64})`)

func containerIDFromCgroup(b []byte) string {
	if m := dockerCgroup.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}

// loadUsers rereads /etc/passwd when its mtime changed.
func (p *Processes) loadUsers() {
	fi, err := os.Stat(p.path("etc", "passwd"))
	if err != nil {
		return
	}
	if p.users != nil && fi.ModTime().Equal(p.userAt) {
		return
	}
	b, err := os.ReadFile(p.path("etc", "passwd"))
	if err != nil {
		return
	}
	users := map[int]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) < 3 {
			continue
		}
		if uid, err := strconv.Atoi(f[2]); err == nil {
			users[uid] = f[0]
		}
	}
	p.users, p.userAt = users, fi.ModTime()
}

func (p *Processes) userName(uid int) string {
	if n, ok := p.users[uid]; ok {
		return n
	}
	return strconv.Itoa(uid)
}
```

Notes for the implementer: `parseMeminfo` and `memInfo.total` already exist in `procfs.go`. `gofmt` the file (the `pidStat` struct alignment above is illustrative).

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/infra/sysmetrics/ && go vet ./internal/infra/sysmetrics/ ./internal/domain/command/`
Expected: PASS, no vet findings.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/command/process.go internal/domain/command/command.go internal/infra/sysmetrics/processes.go internal/infra/sysmetrics/processes_test.go internal/infra/sysmetrics/fixture_test.go
git commit -m "feat(agent): read the host's processes from /proc

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Agent command handler and wiring

**Files:**
- Create: `internal/app/agent/processes.go`
- Test: `internal/app/agent/processes_test.go`
- Modify: `internal/app/agent/dispatcher.go` (struct field, `NewDispatcher` default, `newHandlers` entry)
- Modify: `internal/infra/bootstrap/standalone.go` (~lines 70–97)
- Modify: `cmd/agent/main.go` (~lines 105–123)

**Interfaces:**
- Consumes: `sysmetrics.NewProcesses(root, ncpu)`, `(*sysmetrics.Processes).List()`, `command.ProcessListResponse`, `(*StatusSnapshot).Latest() ([]command.AppStatus, bool)` (each `AppStatus` has `AppID` and `Containers []ContainerStatus{ContainerID ...}` — full 64-hex ids, `docker ps --no-trunc`).
- Produces: `appagent.ProcessSource` interface (`List() (command.ProcessListResponse, error)`), `appagent.Processes` interface (`List(ctx) (command.ProcessListResponse, error)`), `appagent.NewProcessLister(src ProcessSource, status *StatusSnapshot) *ProcessLister` (implements `Processes`), `(*Dispatcher).SetProcesses(p Processes)`, `processListTimeout = 10 * time.Second`.

- [ ] **Step 1: Write the failing tests**

`internal/app/agent/processes_test.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/grpc/proto"
)

type fakeProcSource struct {
	resp  command.ProcessListResponse
	err   error
	block chan struct{}
}

func (f *fakeProcSource) List() (command.ProcessListResponse, error) {
	if f.block != nil {
		<-f.block
	}
	return f.resp, f.err
}

type fixedStatus struct{ apps []command.AppStatus }

func (f fixedStatus) StatusReport(context.Context) (command.GetAppsStatusResponse, error) {
	return command.GetAppsStatusResponse{Apps: f.apps}, nil
}

func TestProcessListerMapsContainersToApps(t *testing.T) {
	snap := NewStatusSnapshot(fixedStatus{apps: []command.AppStatus{
		{AppID: "app-1", Containers: []command.ContainerStatus{{ContainerID: "c1"}}},
	}})
	if _, err := snap.StatusReport(context.Background()); err != nil {
		t.Fatal(err)
	}
	src := &fakeProcSource{resp: command.ProcessListResponse{Processes: []command.Process{
		{PID: 1}, {PID: 2, ContainerID: "c1"}, {PID: 3, ContainerID: "unknown"},
	}}}
	l := NewProcessLister(src, snap)
	l.goos = "linux"
	resp, err := l.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Processes[0].AppID != "" || resp.Processes[1].AppID != "app-1" || resp.Processes[2].AppID != "" {
		t.Fatalf("apps = %+v", resp.Processes)
	}
}

func TestProcessListerNoSnapshotYet(t *testing.T) {
	src := &fakeProcSource{resp: command.ProcessListResponse{Processes: []command.Process{{PID: 2, ContainerID: "c1"}}}}
	l := NewProcessLister(src, NewStatusSnapshot(fixedStatus{}))
	l.goos = "linux"
	if resp, err := l.List(context.Background()); err != nil || resp.Processes[0].AppID != "" {
		t.Fatalf("resp %+v err %v", resp, err)
	}
}

func TestProcessListerNotLinux(t *testing.T) {
	l := NewProcessLister(&fakeProcSource{}, nil)
	l.goos = "darwin"
	if _, err := l.List(context.Background()); !errors.Is(err, errProcessesUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestProcessListerHonoursDeadline(t *testing.T) {
	src := &fakeProcSource{block: make(chan struct{})}
	defer close(src.block)
	l := NewProcessLister(src, nil)
	l.goos = "linux"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := l.List(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestDispatcherProcessList(t *testing.T) {
	d := newTestDispatcher(t)
	// Unwired: a clear error, not a panic.
	if resp := dispatchJSON(t, d, command.TypeProcessList, command.ProcessListRequest{}); resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("unwired process.list succeeded: %+v", resp.Base)
	}
	l := NewProcessLister(&fakeProcSource{resp: command.ProcessListResponse{NCPU: 4, Total: 1,
		Processes: []command.Process{{PID: 7, Name: "x"}}}}, nil)
	l.goos = "linux"
	d.SetProcesses(l)
	resp := dispatchJSON(t, d, command.TypeProcessList, command.ProcessListRequest{})
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("process.list = %+v", resp.Base)
	}
	var out command.ProcessListResponse
	if err := json.Unmarshal(resp.Payload, &out); err != nil || out.NCPU != 4 || len(out.Processes) != 1 || out.Processes[0].PID != 7 {
		t.Fatalf("payload %s err %v", resp.Payload, err)
	}
}
```

Helpers already exist: `dispatchJSON` in `dispatcher_domains_test.go`, `newTestDispatcher` in `dispatcher_test.go`; `resp.Payload` is JSON (other tests `json.Unmarshal` it). `fixedStatus` satisfies `StatusSource` (`reporter.go`: one method, `StatusReport`).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/app/agent/ -run 'TestProcessLister|TestDispatcherProcessList'`
Expected: FAIL — `undefined: NewProcessLister`, `SetProcesses`.

- [ ] **Step 3: Implement**

`internal/app/agent/processes.go`:

```go
package agent

import (
	"context"
	"errors"
	"runtime"
	"time"

	"winterflow/internal/domain/command"
)

// processListTimeout bounds a process.list: it runs on the standalone
// bridge's single goroutine, so it must never hang.
const processListTimeout = 10 * time.Second

var (
	errProcessesUnsupported = errors.New("process list is only available on Linux")
	errNoProcesses          = errors.New("process list is not available on this agent")
)

// ProcessSource reads the host's processes (sysmetrics.Processes).
type ProcessSource interface {
	List() (command.ProcessListResponse, error)
}

// Processes answers process.list (ProcessLister in production).
type Processes interface {
	List(ctx context.Context) (command.ProcessListResponse, error)
}

type noProcesses struct{}

func (noProcesses) List(context.Context) (command.ProcessListResponse, error) {
	return command.ProcessListResponse{}, errNoProcesses
}

// ProcessLister reads the processes and names each container's app from
// the status reporter's last snapshot (no docker call of its own).
type ProcessLister struct {
	src    ProcessSource
	status *StatusSnapshot
	goos   string
}

// NewProcessLister reads through src; status may be nil (no app names).
func NewProcessLister(src ProcessSource, status *StatusSnapshot) *ProcessLister {
	return &ProcessLister{src: src, status: status, goos: runtime.GOOS}
}

// List reads the processes, giving up when ctx ends (the read goes on in
// the background and its answer is dropped; file reads end on their own).
func (l *ProcessLister) List(ctx context.Context) (command.ProcessListResponse, error) {
	if l.goos != "linux" {
		return command.ProcessListResponse{}, errProcessesUnsupported
	}
	type result struct {
		resp command.ProcessListResponse
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := l.src.List()
		ch <- result{resp, err}
	}()
	var r result
	select {
	case r = <-ch:
	case <-ctx.Done():
		return command.ProcessListResponse{}, ctx.Err()
	}
	if r.err != nil {
		return command.ProcessListResponse{}, r.err
	}
	if l.status != nil {
		if apps, ok := l.status.Latest(); ok {
			owner := map[string]string{}
			for _, a := range apps {
				for _, c := range a.Containers {
					owner[c.ContainerID] = a.AppID
				}
			}
			for i := range r.resp.Processes {
				if id := r.resp.Processes[i].ContainerID; id != "" {
					r.resp.Processes[i].AppID = owner[id]
				}
			}
		}
	}
	return r.resp, nil
}
```

`internal/app/agent/dispatcher.go`:
- Add field `processes Processes` to `Dispatcher` (after `backups Backups`).
- In `NewDispatcher`, add `processes: noProcesses{},` to the struct literal.
- Add the setter after `WithReloads`:

```go
// SetProcesses wires process.list (standalone and agent do, before the
// first command can arrive); without it process.list answers an error.
func (d *Dispatcher) SetProcesses(p Processes) { d.processes = p }
```

- In `newHandlers`, before `command.TypeAgentUpdate`:

```go
		command.TypeProcessList: handle(d, func(ctx context.Context, _ command.ProcessListRequest) (command.ProcessListResponse, error) {
			ctx, cancel := context.WithTimeout(ctx, processListTimeout)
			defer cancel()
			return d.processes.List(ctx)
		}),
```

Wiring — `SetProcesses` must run before the bridge starts / the agent connects (the handler reads `d.processes` without a lock), so the status snapshot is created earlier than today:

`internal/infra/bootstrap/standalone.go`: move the line
`statusSnapshot := appagent.NewStatusSnapshot(appagent.WithBackupHealth(orchestrator, backups))`
up to directly after `agentDispatcher.SetBackups(backups)`, then add:

```go
	// The processes tab reads /proc on request; the snapshot names each
	// container's app.
	agentDispatcher.SetProcesses(appagent.NewProcessLister(sysmetrics.NewProcesses("/", runtime.NumCPU()), statusSnapshot))
```

Keep the `go appagent.RunStatusReporter(...)` line and its comment where they are (they use `statusSnapshot`). Add imports `runtime` and `winterflow/internal/infra/sysmetrics`.

`cmd/agent/main.go`: same — move `statusSnapshot := ...` to directly after `dispatcher.SetBackups(backups)`, add `dispatcher.SetProcesses(appagent.NewProcessLister(sysmetrics.NewProcesses("/", runtime.NumCPU()), statusSnapshot))` before `agent.SetDispatcher(dispatcher)`, keep `RunStatusReporter` where it is, add the imports.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go build ./... && go test ./internal/app/agent/ ./internal/infra/bootstrap/ ./cmd/... && go vet ./internal/app/agent/ ./internal/infra/bootstrap/ ./cmd/agent/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/agent/processes.go internal/app/agent/processes_test.go internal/app/agent/dispatcher.go internal/infra/bootstrap/standalone.go cmd/agent/main.go
git commit -m "feat(agent): answer process.list with each container's app

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: API route, contract tests, OpenAPI, docs

**Files:**
- Modify: `internal/app/web/handler/docker/handler.go` (new handler method), `internal/app/web/handler/docker/routes.go` (route entry)
- Test: `internal/app/web/handler/docker/handler_test.go`, `internal/app/web/handler/docker/routes_test.go`
- Modify: `internal/app/web/contract/docker_test.go`, `internal/app/web/contract/matrix_test.go`, `internal/app/web/contract/fakeagent_test.go`
- Regenerate: `api/openapi.yaml` (`make openapi`), `internal/app/web/contract/testdata/parity.golden.json` if `TestParity` asks for it
- Modify: `CLAUDE.md` (implemented command surface)

**Interfaces:**
- Consumes: `command.TypeProcessList`, `command.ProcessListRequest{}`, `command.ProcessListResponse` (Task 1).
- Produces: `GET /api/v1/server/get-processes?server_id=…` → 202 `{data:{request_id}}`; result notification payload = `command.ProcessListResponse`.

- [ ] **Step 1: Write the failing tests**

`internal/app/web/handler/docker/handler_test.go`, in `TestDockerHandlersReturn202`'s case list add:

```go
		{"get-processes", func(h *Handler) http.HandlerFunc { return h.GetProcesses }, "GET", ""},
```

and add:

```go
func TestGetProcessesDispatchesProcessList(t *testing.T) {
	h, fd := newHandler(t)
	h.GetProcesses(httptest.NewRecorder(), authed(httptest.NewRequest("GET", "/x?server_id=s1", nil)))
	if fd.last.Type != command.TypeProcessList {
		t.Fatalf("type = %q", fd.last.Type)
	}
	if _, ok := fd.last.Payload.(command.ProcessListRequest); !ok {
		t.Fatalf("payload = %#v", fd.last.Payload)
	}
}

func TestGetProcessesRejectsForeignServer(t *testing.T) {
	h, fd := newHandler(t)
	w := httptest.NewRecorder()
	h.GetProcesses(w, authed(httptest.NewRequest("GET", "/x?server_id=other", nil)))
	if w.Code != http.StatusForbidden || fd.last.AgentID != "" {
		t.Fatalf("status %d, dispatched %+v", w.Code, fd.last)
	}
}
```

`routes_test.go`: append `"GET /api/v1/server/get-processes"` to `want` (after `"POST /api/v1/agent/update"`).

`internal/app/web/contract/docker_test.go`: append to `cases`:

```go
		{http.MethodGet, "/api/v1/server/get-processes", nil},
```

and in `"agent errors"` add:

```go
		e.agent.fail(command.TypeProcessList, "process list is only available on Linux")
		owner.failWith(t, call{Method: http.MethodGet, Path: "/api/v1/server/get-processes", Query: q},
			"process list is only available on Linux")
```

`matrix_test.go`, beside the `"GET /api/v1/volume/list"` entry:

```go
	"GET /api/v1/server/get-processes": {valid: func(s string) call { return get("/api/v1/server/get-processes", sq(s)) }, malformed: bad("GET", "/api/v1/server/get-processes")},
```

`fakeagent_test.go`: in the reply switch beside `command.TypeVolumeList`:

```go
	case command.TypeProcessList:
		return func(json.RawMessage) reply {
			cpu := 1.5
			return reply{Payload: command.ProcessListResponse{SampledAt: 1700000000000, NCPU: 4, MemTotal: 1 << 30, Total: 1,
				Processes: []command.Process{{PID: 1, Name: "init", User: "root", State: "S", Threads: 1, CPUPct: &cpu, RSS: 1 << 20}}}}
		}
```

and add `command.TypeProcessList` to the list in `TestFakeAgentAnswersEveryCommandType`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/app/web/handler/docker/`
Expected: FAIL — `h.GetProcesses undefined`.

- [ ] **Step 3: Implement**

`internal/app/web/handler/docker/handler.go`, after `UpdateAgent` (add a `// --- processes ---` section):

```go
// --- processes ---

// GetProcesses dispatches a process.list: 202 + request_id; the processes
// arrive over SSE as the command's result. Monitoring's Processes tab polls
// it only while it is open.
func (h *Handler) GetProcesses(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := h.caller(w, r)
	if !ok {
		return
	}
	reqID, err := h.dispatch(r.Context(), userID, serverID, command.TypeProcessList, command.ProcessListRequest{})
	if err != nil {
		webutil.Error(w, "failed to list processes", nil)
		return
	}
	accepted(w, "process list dispatched", reqID)
}
```

Update the package doc comment's first line to mention processes: `// Package docker exposes HTTP handlers for Docker resources (registries, networks and volumes) and the host's process list.`

`routes.go`: build the slice into a variable, then append:

```go
	processes := route(http.MethodGet, "/api/v1/server/get-processes", h.GetProcesses,
		"List the server's processes (CPU, memory, disk I/O)", nil, command.ProcessListResponse{})
	processes.Tag = "metrics"
	return append(routes, processes)
```

(i.e. rename the current `return []api.Route{...}` to `routes := []api.Route{...}`). Extend the `Routes` doc comment: "…plus the server's process list (tag metrics)."

`CLAUDE.md`, in **Implemented command surface**, after `agent — \`agent.update\` (self-update).` change to: ``agent — `agent.update` (self-update); host — `process.list` (Monitoring's Processes tab, polled only while it is open; `/proc` reads, `CAP_SYS_PTRACE` for other users' disk I/O).``

- [ ] **Step 4: Regenerate and run everything touched**

Run:
```bash
make openapi
go test ./internal/app/web/handler/docker/ ./internal/app/web/api/... ./internal/app/web/contract/
```
If `TestParity` reports a golden mismatch caused only by the new route, run `go test ./internal/app/web/contract/ -run TestParity -update` and inspect `git diff internal/app/web/contract/testdata/` (only the new route's entries may change). Then run `make test-pg` if Docker is available (both databases must match the golden); if not available, say so in the report.
Expected: PASS; `TestOpenAPIUpToDate` passes.

- [ ] **Step 5: Commit**

```bash
git add internal/app/web/handler/docker internal/app/web/contract api/openapi.yaml CLAUDE.md
git commit -m "feat(api): GET /api/v1/server/get-processes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Installer grants CAP_SYS_PTRACE

**Files:**
- Modify: `scripts/install.sh` (~lines 366–368)
- Test: `scripts/install_test.sh` (~line 154)

- [ ] **Step 1: Write the failing test**

`scripts/install_test.sh`, replace the line asserting `AmbientCapabilities=CAP_NET_BIND_SERVICE` with:

```bash
assert_contains "unit: bind 80/443 + read process I/O" "$out" "AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_SYS_PTRACE"
assert_contains "unit: capability bounding set" "$out" "CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_SYS_PTRACE"
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bash scripts/install_test.sh`
Expected: FAIL on "unit: bind 80/443 + read process I/O".

- [ ] **Step 3: Implement**

`scripts/install.sh`, replace:

```
# Allow binding the ingress ports 80/443 without root.
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
```

with:

```
# Allow binding the ingress ports 80/443 without root, and reading every
# process's disk I/O (/proc/<pid>/io) for Monitoring's Processes tab. The
# agent already holds the Docker socket, which is root-equivalent.
AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_SYS_PTRACE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_SYS_PTRACE
```

- [ ] **Step 4: Run it to verify it passes**

Run: `bash scripts/install_test.sh`
Expected: all assertions pass.

- [ ] **Step 5: Commit**

```bash
git add scripts/install.sh scripts/install_test.sh
git commit -m "feat(install): CAP_SYS_PTRACE for per-process disk I/O

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Web — process list helpers and the polling hook

**Files:**
- Create: `web/src/lib/processes.ts`, `web/src/lib/processes.test.ts`
- Create: `web/src/hooks/use-page-visible.ts`
- Create: `web/src/hooks/use-processes.ts`, `web/src/hooks/use-processes.test.tsx`

**Interfaces:**
- Consumes: `GET /api/v1/server/get-processes?server_id=…` → `{data:{request_id}}`; `useNotifications().waitFor(ref, timeoutMs)` → `Notification{status, payload, error}`; `WaitTimeoutError` from `@/context/notifications-context-base`; `readApiError` from `@/lib/api`; `apiBaseUrl` from `@/config`.
- Produces (used by Task 6):
  - `type ProcessInfo`, `type ProcessList` (mirror `command.Process` / `ProcessListResponse`)
  - `type SortKey = "name" | "app" | "user" | "pid" | "cpu" | "mem" | "read" | "write" | "threads" | "uptime"`, `type SortDir = "asc" | "desc"`
  - `type AppFilter = "all" | "host" | \`app:${string}\``
  - `filterProcesses(list: ProcessInfo[], f: { text: string; app: AppFilter; kernel: boolean }, appName: (id: string) => string | undefined): ProcessInfo[]`
  - `sortProcesses(list: ProcessInfo[], key: SortKey, dir: SortDir, appName: (id: string) => string | undefined): ProcessInfo[]`
  - `defaultDir(key: SortKey): SortDir`
  - `formatUptime(startUnixS: number, nowMs: number): string`
  - `usePageVisible(): boolean`
  - `useProcesses(serverId: string | null, enabled: boolean): { data: ProcessList | null; error: string | null; updatedAt: number | null; refresh: () => void }` (`refresh` cuts the current 5 s wait short; a no-op while a request is in flight or polling is off), constants `PROCESS_POLL_MS = 5000`, `PROCESS_WAIT_MS = 15000`

- [ ] **Step 1: Write the failing tests**

`web/src/lib/processes.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import { defaultDir, filterProcesses, formatUptime, sortProcesses, type ProcessInfo } from "@/lib/processes";

const p = (o: Partial<ProcessInfo>): ProcessInfo => ({
  pid: 1, ppid: 0, name: "x", cmdline: "", user: "root", state: "S", threads: 1, start_time: 0,
  kernel: false, cpu_pct: null, rss: 0, mem_pct: 0, read_bps: null, write_bps: null, ...o,
});

const names: Record<string, string> = { "app-1": "Nextcloud" };
const appName = (id: string) => names[id];

const LIST = [
  p({ pid: 10, name: "postgres", cmdline: "postgres: writer", user: "alice", cpu_pct: 5, rss: 300, write_bps: 100, app_id: "app-1" }),
  p({ pid: 2, name: "kthreadd", kernel: true }),
  p({ pid: 7, name: "sshd", cmdline: "/usr/sbin/sshd -D", cpu_pct: 20, rss: 100 }),
  p({ pid: 8, name: "bash", cpu_pct: 20, rss: 50 }),
];

describe("filterProcesses", () => {
  const all = { text: "", app: "all" as const, kernel: false };
  it("hides kernel threads unless asked", () => {
    expect(filterProcesses(LIST, all, appName).map((x) => x.pid)).toEqual([10, 7, 8]);
    expect(filterProcesses(LIST, { ...all, kernel: true }, appName)).toHaveLength(4);
  });
  it("matches name, command, user, pid and app name, case-insensitively", () => {
    const pids = (text: string) => filterProcesses(LIST, { ...all, text }, appName).map((x) => x.pid);
    expect(pids("SSHD")).toEqual([7]);
    expect(pids("writer")).toEqual([10]);
    expect(pids("alice")).toEqual([10]);
    expect(pids("8")).toEqual([8]);
    expect(pids("nextcloud")).toEqual([10]);
    expect(pids("  ")).toEqual([10, 7, 8]);
  });
  it("narrows to one app or to host processes", () => {
    expect(filterProcesses(LIST, { ...all, app: "app:app-1" }, appName).map((x) => x.pid)).toEqual([10]);
    expect(filterProcesses(LIST, { ...all, app: "host" }, appName).map((x) => x.pid)).toEqual([7, 8]);
  });
});

describe("sortProcesses", () => {
  it("sorts by a number, ties by pid, unknown values last in both directions", () => {
    expect(sortProcesses(LIST, "cpu", "desc", appName).map((x) => x.pid)).toEqual([7, 8, 10, 2]);
    expect(sortProcesses(LIST, "cpu", "asc", appName).map((x) => x.pid)).toEqual([10, 7, 8, 2]);
    expect(sortProcesses(LIST, "write", "desc", appName).map((x) => x.pid)).toEqual([10, 2, 7, 8]);
  });
  it("sorts text case-insensitively and apps by their name", () => {
    expect(sortProcesses(LIST, "name", "asc", appName).map((x) => x.name)).toEqual(["bash", "kthreadd", "postgres", "sshd"]);
    expect(sortProcesses(LIST, "app", "asc", appName)[0].pid).toBe(10);
  });
  it("uptime: the newest process has the least", () => {
    const l = [p({ pid: 1, start_time: 100 }), p({ pid: 2, start_time: 200 })];
    expect(sortProcesses(l, "uptime", "desc", appName).map((x) => x.pid)).toEqual([1, 2]);
  });
  it("does not change its input", () => {
    const copy = [...LIST];
    sortProcesses(LIST, "pid", "asc", appName);
    expect(LIST).toEqual(copy);
  });
});

describe("defaultDir and formatUptime", () => {
  it("numbers start descending, text ascending", () => {
    expect(defaultDir("cpu")).toBe("desc");
    expect(defaultDir("name")).toBe("asc");
    expect(defaultDir("pid")).toBe("asc");
  });
  it("formats an age", () => {
    const now = 1_700_000_000_000;
    expect(formatUptime(now / 1000 - 42, now)).toBe("42s");
    expect(formatUptime(now / 1000 - 3 * 60, now)).toBe("3m");
    expect(formatUptime(now / 1000 - (2 * 3600 + 5 * 60), now)).toBe("2h 5m");
    expect(formatUptime(now / 1000 - (3 * 86400 + 4 * 3600), now)).toBe("3d 4h");
    expect(formatUptime(0, now)).toBe("—");
  });
});
```

`web/src/hooks/use-processes.test.tsx`:

```tsx
import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { NotificationsContext, WaitTimeoutError, type Notification, type NotificationsContextValue } from "@/context/notifications-context-base";
import { PROCESS_POLL_MS, useProcesses } from "@/hooks/use-processes";
import { fakeAPI } from "@/test/fake-api";

const waitFor_ = vi.fn<(ref: string, ms?: number) => Promise<Notification>>();
const ctx = { connected: true, subscribe: () => () => {}, waitFor: waitFor_ } as unknown as NotificationsContextValue;
const wrapper = ({ children }: { children: ReactNode }) => (
  <NotificationsContext.Provider value={ctx}>{children}</NotificationsContext.Provider>
);
const LIST = { sampled_at: 1, ncpu: 4, mem_total: 1, total: 1, processes: [{ pid: 1 }] };

describe("useProcesses", () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    waitFor_.mockReset().mockResolvedValue({ type: "operation_result", ref: "r1", status: 0, payload: LIST });
  });
  afterEach(() => vi.useRealTimers());

  it("does nothing while disabled", async () => {
    const api = fakeAPI({ "/api/v1/server/get-processes": { request_id: "r1" } });
    renderHook(() => useProcesses("s1", false), { wrapper });
    await act(() => vi.advanceTimersByTimeAsync(PROCESS_POLL_MS * 3));
    expect(api.calls).toHaveLength(0);
  });

  it("polls one request at a time, 5 s apart, and waits 15 s for each", async () => {
    const api = fakeAPI({ "/api/v1/server/get-processes": { request_id: "r1" } });
    const { result } = renderHook(() => useProcesses("s1", true), { wrapper });
    await waitFor(() => expect(result.current.data?.ncpu).toBe(4));
    expect(api.calls[0].query).toBe("?server_id=s1");
    expect(waitFor_).toHaveBeenCalledWith("r1", 15000);
    await act(() => vi.advanceTimersByTimeAsync(PROCESS_POLL_MS - 100));
    expect(api.calls).toHaveLength(1);
    await act(() => vi.advanceTimersByTimeAsync(200));
    await waitFor(() => expect(api.calls).toHaveLength(2));
  });

  it("stops polling when disabled", async () => {
    const api = fakeAPI({ "/api/v1/server/get-processes": { request_id: "r1" } });
    const { rerender } = renderHook(({ on }) => useProcesses("s1", on), { wrapper, initialProps: { on: true } });
    await waitFor(() => expect(api.calls).toHaveLength(1));
    rerender({ on: false });
    await act(() => vi.advanceTimersByTimeAsync(PROCESS_POLL_MS * 3));
    expect(api.calls).toHaveLength(1);
  });

  it("drops a late reply after the server changed", async () => {
    fakeAPI({ "/api/v1/server/get-processes": { request_id: "r1" } });
    let release!: (n: Notification) => void;
    waitFor_.mockImplementationOnce(() => new Promise((r) => (release = r)));
    const { result, rerender } = renderHook(({ id }) => useProcesses(id, true), { wrapper, initialProps: { id: "s1" } });
    await waitFor(() => expect(waitFor_).toHaveBeenCalledTimes(1));
    rerender({ id: "s2" });
    await act(async () => release({ type: "operation_result", ref: "r1", status: 0, payload: { ...LIST, ncpu: 99 } }));
    await waitFor(() => expect(result.current.data?.ncpu).toBe(4)); // s2's own reply
    expect(result.current.data?.ncpu).not.toBe(99);
  });

  it("shows a timeout and keeps polling, keeping the last good data", async () => {
    const api = fakeAPI({ "/api/v1/server/get-processes": { request_id: "r1" } });
    const { result } = renderHook(() => useProcesses("s1", true), { wrapper });
    await waitFor(() => expect(result.current.data).not.toBeNull());
    waitFor_.mockRejectedValueOnce(new WaitTimeoutError());
    await act(() => vi.advanceTimersByTimeAsync(PROCESS_POLL_MS + 10));
    await waitFor(() => expect(result.current.error).toBe("The server did not answer in time."));
    expect(result.current.data?.ncpu).toBe(4);
    await act(() => vi.advanceTimersByTimeAsync(PROCESS_POLL_MS + 10));
    await waitFor(() => expect(api.calls.length).toBeGreaterThanOrEqual(3));
    await waitFor(() => expect(result.current.error).toBeNull());
  });

  it("refresh cuts the wait short", async () => {
    const api = fakeAPI({ "/api/v1/server/get-processes": { request_id: "r1" } });
    const { result } = renderHook(() => useProcesses("s1", true), { wrapper });
    await waitFor(() => expect(result.current.data).not.toBeNull());
    act(() => result.current.refresh());
    await waitFor(() => expect(api.calls).toHaveLength(2));
  });

  it("reports the agent's error", async () => {
    fakeAPI({ "/api/v1/server/get-processes": { request_id: "r1" } });
    waitFor_.mockResolvedValue({ type: "operation_result", ref: "r1", status: 1, error: "process list is only available on Linux" });
    const { result } = renderHook(() => useProcesses("s1", true), { wrapper });
    await waitFor(() => expect(result.current.error).toBe("process list is only available on Linux"));
  });
});
```

(If `vi.useFakeTimers` with `shouldAdvanceTime` conflicts with Testing Library's `waitFor` in this repo, check how `web/src/hooks/use-rollout.test.tsx` drives timers and copy that setup.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/processes.test.ts src/hooks/use-processes.test.tsx`
Expected: FAIL — modules not found.

- [ ] **Step 3: Implement**

`web/src/lib/processes.ts`:

```ts
// ProcessInfo mirrors command.Process (process.list's result). Rates are
// null when unknown (started between readings, or /proc/<pid>/io not
// readable to the agent).
export type ProcessInfo = {
  pid: number;
  ppid: number;
  name: string;
  cmdline: string;
  user: string;
  state: string;
  threads: number;
  start_time: number; // unix s
  kernel: boolean;
  cpu_pct: number | null; // per core, may exceed 100
  rss: number;
  mem_pct: number;
  read_bps: number | null;
  write_bps: number | null;
  container_id?: string;
  app_id?: string;
};

export type ProcessList = {
  sampled_at: number;
  ncpu: number;
  mem_total: number;
  total: number;
  processes: ProcessInfo[];
};

export type SortKey = "name" | "app" | "user" | "pid" | "cpu" | "mem" | "read" | "write" | "threads" | "uptime";
export type SortDir = "asc" | "desc";
export type AppFilter = "all" | "host" | `app:${string}`;
export type ProcessFilter = { text: string; app: AppFilter; kernel: boolean };

type AppName = (id: string) => string | undefined;

const TEXT_KEYS: ReadonlySet<SortKey> = new Set(["name", "app", "user", "pid"]);

// defaultDir is a column's first sort direction: amounts biggest first,
// names (and pids) in order.
export function defaultDir(key: SortKey): SortDir {
  return TEXT_KEYS.has(key) ? "asc" : "desc";
}

export function filterProcesses(list: ProcessInfo[], f: ProcessFilter, appName: AppName): ProcessInfo[] {
  const q = f.text.trim().toLowerCase();
  return list.filter((p) => {
    if (!f.kernel && p.kernel) return false;
    if (f.app === "host" && p.app_id) return false;
    if (f.app.startsWith("app:") && p.app_id !== f.app.slice(4)) return false;
    if (!q) return true;
    const app = p.app_id ? (appName(p.app_id) ?? p.app_id) : "";
    return [p.name, p.cmdline, p.user, String(p.pid), app].some((s) => s.toLowerCase().includes(q));
  });
}

function value(p: ProcessInfo, key: SortKey, appName: AppName): number | string | null {
  switch (key) {
    case "name":
      return p.name.toLowerCase();
    case "app":
      return p.app_id ? (appName(p.app_id) ?? p.app_id).toLowerCase() : null;
    case "user":
      return p.user.toLowerCase();
    case "pid":
      return p.pid;
    case "cpu":
      return p.cpu_pct;
    case "mem":
      return p.rss;
    case "read":
      return p.read_bps;
    case "write":
      return p.write_bps;
    case "threads":
      return p.threads;
    case "uptime":
      // Older start = longer uptime.
      return p.start_time > 0 ? -p.start_time : null;
  }
}

// sortProcesses returns a sorted copy. Unknown values (null) go last in
// either direction; ties keep pid order so rows do not jump between polls.
export function sortProcesses(list: ProcessInfo[], key: SortKey, dir: SortDir, appName: AppName): ProcessInfo[] {
  const sign = dir === "asc" ? 1 : -1;
  return [...list].sort((a, b) => {
    const va = value(a, key, appName);
    const vb = value(b, key, appName);
    if (va === null || vb === null) {
      if (va !== vb) return va === null ? 1 : -1;
    } else if (va !== vb) {
      return (va < vb ? -1 : 1) * sign;
    }
    return a.pid - b.pid;
  });
}

// formatUptime is how long ago a process started: 42s, 3m, 2h 5m, 3d 4h.
export function formatUptime(startUnixS: number, nowMs: number): string {
  if (!startUnixS) return "—";
  const s = Math.max(0, Math.floor(nowMs / 1000 - startUnixS));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}
```

`web/src/hooks/use-page-visible.ts`:

```ts
import { useSyncExternalStore } from "react";

function subscribe(onChange: () => void) {
  document.addEventListener("visibilitychange", onChange);
  return () => document.removeEventListener("visibilitychange", onChange);
}

// usePageVisible reports whether the page is visible (not a background
// tab, not a minimized window).
export function usePageVisible(): boolean {
  return useSyncExternalStore(subscribe, () => document.visibilityState === "visible", () => true);
}
```

`web/src/hooks/use-processes.ts`:

```ts
import { useCallback, useEffect, useRef, useState } from "react";

import { apiBaseUrl } from "@/config";
import { WaitTimeoutError } from "@/context/notifications-context-base";
import { useNotifications } from "@/context/use-notifications";
import { readApiError } from "@/lib/api";
import type { ProcessList } from "@/lib/processes";

export const PROCESS_POLL_MS = 5000;
export const PROCESS_WAIT_MS = 15000;

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

type State = { serverId: string | null; data: ProcessList | null; error: string | null; updatedAt: number | null };

// useProcesses polls the server's process list while enabled: one request
// at a time (dispatch, wait for its SSE result, then 5 s), so a slow agent
// never piles requests up. Disabling, unmounting or another server stops
// the loop; a reply that arrives after that is dropped.
export function useProcesses(serverId: string | null, enabled: boolean) {
  const { waitFor } = useNotifications();
  const [state, setState] = useState<State>({ serverId: null, data: null, error: null, updatedAt: null });
  // wake ends the loop's current wait (Refresh); a no-op otherwise.
  const wake = useRef<() => void>(() => {});

  useEffect(() => {
    if (!serverId || !enabled) return;
    let live = true;
    const ctl = new AbortController();
    let timer: number | undefined;
    let resolveSleep: (() => void) | undefined;
    const sleep = (ms: number) =>
      new Promise<void>((r) => {
        resolveSleep = r;
        timer = window.setTimeout(r, ms);
      });
    wake.current = () => {
      window.clearTimeout(timer);
      resolveSleep?.();
    };
    const update = (patch: Partial<State>) =>
      setState((cur) => ({ ...(cur.serverId === serverId ? cur : { data: null, error: null, updatedAt: null }), ...patch, serverId }));

    void (async () => {
      while (live) {
        try {
          const res = await fetch(`${base}/api/v1/server/get-processes?server_id=${encodeURIComponent(serverId)}`, {
            credentials: "include",
            signal: ctl.signal,
          });
          if (!res.ok) throw await readApiError(res);
          const accepted = (await res.json()) as { data?: { request_id?: string } };
          const ref = accepted.data?.request_id;
          if (!ref) throw new Error("No request id");
          const n = await waitFor(ref, PROCESS_WAIT_MS);
          if (!live) return;
          if (n.status && n.status !== 0) throw new Error(n.error || "Failed to list processes");
          update({ data: n.payload as ProcessList, error: null, updatedAt: Date.now() });
        } catch (e) {
          if (!live) return;
          update({
            error: e instanceof WaitTimeoutError ? "The server did not answer in time." : e instanceof Error ? e.message : "Failed to list processes",
          });
        }
        if (!live) return;
        await sleep(PROCESS_POLL_MS);
      }
    })();

    return () => {
      live = false;
      ctl.abort();
      window.clearTimeout(timer);
      wake.current = () => {};
    };
  }, [serverId, enabled, waitFor]);

  const refresh = useCallback(() => wake.current(), []);
  const mine = state.serverId === serverId;
  return { data: mine ? state.data : null, error: mine ? state.error : null, updatedAt: mine ? state.updatedAt : null, refresh };
}
```

Check: `useNotifications` lives at `@/context/use-notifications` (as `app-logs-tab.tsx` imports it); `readApiError` returns an `Error`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/processes.test.ts src/hooks/use-processes.test.tsx && pnpm --dir web run lint && pnpm --dir web exec tsc -b`
Expected: PASS, no lint or type errors.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/processes.ts web/src/lib/processes.test.ts web/src/hooks/use-page-visible.ts web/src/hooks/use-processes.ts web/src/hooks/use-processes.test.tsx
git commit -m "feat(web): process list helpers and a visibility-aware poller

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Web — Processes tab on the Monitoring page

**Files:**
- Create: `web/src/components/monitoring/processes-tab.tsx`, `web/src/components/monitoring/processes-tab.test.tsx`
- Modify: `web/src/pages/monitoring.tsx` (wrap the body in tabs)
- Test: `web/src/pages/monitoring.test.tsx` (tab switching)

**Interfaces:**
- Consumes (Task 5): `useProcesses`, `usePageVisible`, `filterProcesses`, `sortProcesses`, `defaultDir`, `formatUptime`, types. Existing: `UnderlineTabs` (`@/components/underline-tabs`), `TabsContent` (`@/components/ui/tabs`), `useTabParam` (`@/hooks/use-tab-param`), `DataFreshness`, `formatPercent`/`formatSize`/`formatRate` (`@/lib/metrics`), `appLabel` (`@/lib/kpi`), `appPath` (`@/lib/routes`), `useApps`, shadcn `Table*`, `Input`, `Label`, `Select*`, `Switch`, `Button`, `Skeleton`.
- Produces: `ProcessesTab({ serverId }: { serverId: string })`.

- [ ] **Step 1: Write the failing tests**

`web/src/components/monitoring/processes-tab.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ProcessesTab } from "@/components/monitoring/processes-tab";
import type { ProcessInfo, ProcessList } from "@/lib/processes";

const useProcesses = vi.fn();
let result: { data: ProcessList | null; error: string | null; updatedAt: number | null; refresh: () => void };
vi.mock("@/hooks/use-processes", () => ({
  useProcesses: (...a: unknown[]) => {
    useProcesses(...a);
    return result;
  },
}));
let visible = true;
vi.mock("@/hooks/use-page-visible", () => ({ usePageVisible: () => visible }));
vi.mock("@/context/use-apps", () => ({ useApps: () => ({ apps: [{ id: "app-1", name: "Nextcloud" }] }) }));

const p = (o: Partial<ProcessInfo>): ProcessInfo => ({
  pid: 1, ppid: 0, name: "x", cmdline: "", user: "root", state: "S", threads: 1, start_time: 0,
  kernel: false, cpu_pct: 0, rss: 0, mem_pct: 0, read_bps: 0, write_bps: 0, ...o,
});

const LIST: ProcessList = {
  sampled_at: 0, ncpu: 4, mem_total: 1 << 30, total: 4,
  processes: [
    p({ pid: 10, name: "postgres", cpu_pct: 5, app_id: "app-1", write_bps: null, read_bps: null }),
    p({ pid: 7, name: "sshd", cpu_pct: 20 }),
    p({ pid: 2, name: "kthreadd", kernel: true }),
    p({ pid: 8, name: "bash", cpu_pct: 1 }),
  ],
};

function names() {
  return within(screen.getByRole("table")).getAllByTestId("process-name").map((n) => n.textContent);
}

function renderTab() {
  return render(<MemoryRouter><ProcessesTab serverId="s1" /></MemoryRouter>);
}

describe("ProcessesTab", () => {
  beforeEach(() => {
    useProcesses.mockReset();
    visible = true;
    result = { data: LIST, error: null, updatedAt: Date.now(), refresh: vi.fn() };
  });

  it("polls only while the page is visible and not paused", async () => {
    const user = userEvent.setup();
    const { rerender } = renderTab();
    expect(useProcesses).toHaveBeenLastCalledWith("s1", true);
    await user.click(screen.getByRole("button", { name: "Pause" }));
    expect(useProcesses).toHaveBeenLastCalledWith("s1", false);
    await user.click(screen.getByRole("button", { name: "Resume" }));
    visible = false;
    rerender(<MemoryRouter><ProcessesTab serverId="s1" /></MemoryRouter>);
    expect(useProcesses).toHaveBeenLastCalledWith("s1", false);
  });

  it("sorts by CPU, highest first, and hides kernel threads by default", () => {
    renderTab();
    expect(names()).toEqual(["sshd", "postgres", "bash"]);
    expect(screen.getByText(/4 cores/)).toBeInTheDocument();
  });

  it("re-sorts on a header click and flips on a second click", async () => {
    const user = userEvent.setup();
    renderTab();
    await user.click(screen.getByRole("button", { name: "Sort by Process" }));
    expect(names()).toEqual(["bash", "postgres", "sshd"]);
    await user.click(screen.getByRole("button", { name: "Sort by Process" }));
    expect(names()).toEqual(["sshd", "postgres", "bash"]);
  });

  it("keeps the chosen sort across a refresh", async () => {
    const user = userEvent.setup();
    const { rerender } = renderTab();
    await user.click(screen.getByRole("button", { name: "Sort by Process" }));
    result = { ...result, data: { ...LIST, processes: [...LIST.processes, p({ pid: 3, name: "aaa", cpu_pct: 90 })] } };
    rerender(<MemoryRouter><ProcessesTab serverId="s1" /></MemoryRouter>);
    expect(names()[0]).toBe("aaa");
  });

  it("filters by text, by app, and shows kernel threads on request", async () => {
    const user = userEvent.setup();
    renderTab();
    await user.type(screen.getByRole("searchbox", { name: "Filter processes" }), "nextcloud");
    expect(names()).toEqual(["postgres"]);
    await user.clear(screen.getByRole("searchbox", { name: "Filter processes" }));
    await user.click(screen.getByRole("switch", { name: "Show kernel threads" }));
    expect(names()).toContain("kthreadd");
    await user.type(screen.getByRole("searchbox", { name: "Filter processes" }), "zzz");
    expect(screen.getByText("No processes match.")).toBeInTheDocument();
  });

  it("renders a dash for unknown rates and links the app", () => {
    renderTab();
    const row = screen.getAllByTestId("process-row").find((r) => within(r).queryByText("postgres"))!;
    expect(within(row).getAllByText("—").length).toBeGreaterThanOrEqual(2);
    expect(within(row).getByRole("link", { name: "Nextcloud" })).toHaveAttribute("href", "/server/s1/app/app-1");
  });

  it("shows skeleton rows before the first answer and an inline error after", () => {
    result = { data: null, error: null, updatedAt: null, refresh: vi.fn() };
    const { rerender } = renderTab();
    expect(screen.getByTestId("processes-loading")).toBeInTheDocument();
    result = { data: LIST, error: "The server did not answer in time.", updatedAt: Date.now(), refresh: vi.fn() };
    rerender(<MemoryRouter><ProcessesTab serverId="s1" /></MemoryRouter>);
    expect(screen.getByText("The server did not answer in time.")).toBeInTheDocument();
    expect(names()).toHaveLength(3); // the last good list stays
  });

  it("says when the list is cut", () => {
    result = { ...result, data: { ...LIST, total: 2500 } };
    renderTab();
    expect(screen.getByText(/busiest 4 of 2500/)).toBeInTheDocument();
  });
});
```

Verify `appPath("s1", "app-1")` returns `/server/s1/app/app-1` (see `web/src/lib/routes.ts:39`); adjust the expected href to its actual output.

`web/src/pages/monitoring.test.tsx`: add a mock and a test:

```tsx
vi.mock("@/components/monitoring/processes-tab", () => ({
  ProcessesTab: (p: { serverId: string }) => <div data-testid="processes-tab">{p.serverId}</div>,
}));
```

```tsx
  it("opens the Processes tab, mounted only while selected", async () => {
    const user = userEvent.setup();
    renderPage();
    expect(screen.queryByTestId("processes-tab")).toBeNull();
    await user.click(screen.getByRole("tab", { name: "Processes" }));
    expect(screen.getByTestId("processes-tab")).toHaveTextContent("s1");
    await user.click(screen.getByRole("tab", { name: "Overview" }));
    expect(screen.queryByTestId("processes-tab")).toBeNull();
  });
```

(Place the `it` inside the file's existing top-level `describe`; reuse its `beforeEach` data setup so the Overview renders.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/monitoring/processes-tab.test.tsx src/pages/monitoring.test.tsx`
Expected: FAIL — module not found / no "Processes" tab.

- [ ] **Step 3: Implement the tab**

`web/src/components/monitoring/processes-tab.tsx`:

```tsx
import { Fragment, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { ArrowDown, ArrowUp, Pause, Play } from "lucide-react";

import { DataFreshness } from "@/components/data-freshness";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useApps } from "@/context/use-apps";
import { usePageVisible } from "@/hooks/use-page-visible";
import { useProcesses } from "@/hooks/use-processes";
import { appLabel } from "@/lib/kpi";
import { formatPercent, formatRate, formatSize } from "@/lib/metrics";
import {
  defaultDir,
  filterProcesses,
  formatUptime,
  sortProcesses,
  type AppFilter,
  type ProcessInfo,
  type SortDir,
  type SortKey,
} from "@/lib/processes";
import { appPath } from "@/lib/routes";
import { cn } from "@/lib/utils";

// Column order; wide is shown from md up (on a phone the row expands).
const COLUMNS: { key: SortKey; label: string; wide?: boolean; numeric?: boolean }[] = [
  { key: "name", label: "Process" },
  { key: "app", label: "App", wide: true },
  { key: "user", label: "User", wide: true },
  { key: "pid", label: "PID", wide: true, numeric: true },
  { key: "cpu", label: "CPU", numeric: true },
  { key: "mem", label: "Memory", numeric: true },
  { key: "read", label: "Read/s", wide: true, numeric: true },
  { key: "write", label: "Write/s", numeric: true },
  { key: "threads", label: "Threads", wide: true, numeric: true },
  { key: "uptime", label: "Uptime", wide: true, numeric: true },
];
const WIDE = "hidden md:table-cell";

const rate = (v: number | null) => (v === null ? "—" : formatRate(v));

// ProcessesTab lists the server's processes, polled only while this tab is
// mounted (Monitoring mounts it only while selected), the page is visible
// and the user has not paused it.
export function ProcessesTab({ serverId }: { serverId: string }) {
  const { apps } = useApps();
  const visible = usePageVisible();
  const [paused, setPaused] = useState(false);
  const { data, error, updatedAt, refresh } = useProcesses(serverId, visible && !paused);

  const [sort, setSort] = useState<{ key: SortKey; dir: SortDir }>({ key: "cpu", dir: "desc" });
  const [text, setText] = useState("");
  const [app, setApp] = useState<AppFilter>("all");
  const [kernel, setKernel] = useState(false);
  const [open, setOpen] = useState<number | null>(null);

  const appName = useMemo(() => {
    const names = new Map(apps.map((a) => [a.id, appLabel(a.name)]));
    return (id: string) => names.get(id);
  }, [apps]);
  const rows = useMemo(
    () => sortProcesses(filterProcesses(data?.processes ?? [], { text, app, kernel }, appName), sort.key, sort.dir, appName),
    [data, text, app, kernel, appName, sort],
  );
  const appIds = useMemo(
    () => [...new Set((data?.processes ?? []).flatMap((p) => (p.app_id ? [p.app_id] : [])))],
    [data],
  );
  const now = data?.sampled_at || Date.now();

  const onSort = (key: SortKey) =>
    setSort((cur) => (cur.key === key ? { key, dir: cur.dir === "asc" ? "desc" : "asc" } : { key, dir: defaultDir(key) }));

  const cell = (p: ProcessInfo, key: SortKey) => {
    switch (key) {
      case "name":
        return (
          <div className="min-w-0 max-w-[18rem]">
            <div data-testid="process-name" className="truncate font-medium">{p.name}</div>
            {p.cmdline ? (
              <div className="truncate text-xs text-muted-foreground" title={p.cmdline}>{p.cmdline}</div>
            ) : null}
          </div>
        );
      case "app":
        return p.app_id ? (
          <Link
            to={appPath(serverId, p.app_id)}
            onClick={(e) => e.stopPropagation()}
            className="inline-flex max-w-40 truncate rounded-md bg-secondary px-2 py-0.5 text-xs font-medium text-secondary-foreground hover:underline"
          >
            {appName(p.app_id) ?? p.app_id}
          </Link>
        ) : null;
      case "user":
        return p.user;
      case "pid":
        return p.pid;
      case "cpu":
        return p.cpu_pct === null ? "—" : formatPercent(p.cpu_pct);
      case "mem":
        return (
          <>
            {formatSize(p.rss)}
            <span className="ml-1 text-xs text-muted-foreground">{formatPercent(p.mem_pct)}</span>
          </>
        );
      case "read":
        return rate(p.read_bps);
      case "write":
        return rate(p.write_bps);
      case "threads":
        return p.threads;
      case "uptime":
        return formatUptime(p.start_time, now);
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <Input
          type="search"
          aria-label="Filter processes"
          placeholder="Filter by name, command, user, PID or app"
          value={text}
          onChange={(e) => setText(e.target.value)}
          className="w-full sm:w-72"
        />
        <Select value={app} onValueChange={(v) => setApp(v as AppFilter)}>
          <SelectTrigger aria-label="App" className="w-44">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All processes</SelectItem>
            <SelectItem value="host">Host only</SelectItem>
            {appIds.map((id) => (
              <SelectItem key={id} value={`app:${id}`}>{appName(id) ?? id}</SelectItem>
            ))}
          </SelectContent>
        </Select>
        <div className="flex items-center gap-2">
          <Switch id="kernel-threads" checked={kernel} onCheckedChange={setKernel} aria-label="Show kernel threads" />
          <Label htmlFor="kernel-threads" className="text-sm font-normal">Kernel threads</Label>
        </div>
        <div className="ml-auto flex items-center gap-2 text-sm text-muted-foreground">
          {data ? (
            <span>
              {data.ncpu} cores · CPU is per core
              {data.total > data.processes.length ? ` · busiest ${data.processes.length} of ${data.total}` : ""}
            </span>
          ) : null}
          <DataFreshness
            updatedAt={updatedAt}
            onRefresh={() => {
              setPaused(false);
              refresh();
            }}
          />
          <Button variant="outline" size="sm" onClick={() => setPaused((x) => !x)} aria-label={paused ? "Resume" : "Pause"}>
            {paused ? <Play /> : <Pause />}
            {paused ? "Resume" : "Pause"}
          </Button>
        </div>
      </div>

      {error ? <p className="text-sm text-destructive">{error}</p> : null}

      {!data ? (
        <div data-testid="processes-loading" className="space-y-2">
          {Array.from({ length: 8 }, (_, i) => (
            <Skeleton key={i} className="h-9 w-full" />
          ))}
        </div>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              {COLUMNS.map((c) => {
                const active = sort.key === c.key;
                return (
                  <TableHead
                    key={c.key}
                    aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}
                    className={cn(c.wide && WIDE, c.numeric && "text-right")}
                  >
                    <button
                      type="button"
                      aria-label={`Sort by ${c.label}`}
                      onClick={() => onSort(c.key)}
                      className={cn("inline-flex items-center gap-1 hover:text-foreground", active && "text-foreground")}
                    >
                      {c.label}
                      {active ? sort.dir === "asc" ? <ArrowUp className="size-3" /> : <ArrowDown className="size-3" /> : null}
                    </button>
                  </TableHead>
                );
              })}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={COLUMNS.length} className="py-8 text-center text-muted-foreground">
                  No processes match.
                </TableCell>
              </TableRow>
            ) : (
              rows.map((p) => (
                <Fragment key={p.pid}>
                  <TableRow
                    data-testid="process-row"
                    onClick={() => setOpen((cur) => (cur === p.pid ? null : p.pid))}
                    className="cursor-pointer md:cursor-default"
                  >
                    {COLUMNS.map((c) => (
                      <TableCell key={c.key} className={cn(c.wide && WIDE, c.numeric && "text-right tabular-nums")}>
                        {cell(p, c.key)}
                      </TableCell>
                    ))}
                  </TableRow>
                  {open === p.pid ? (
                    <TableRow className="md:hidden">
                      <TableCell colSpan={COLUMNS.length}>
                        <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
                          {COLUMNS.filter((c) => c.wide).map((c) => (
                            <Fragment key={c.key}>
                              <dt className="text-muted-foreground">{c.label}</dt>
                              <dd className="tabular-nums">{cell(p, c.key) ?? "—"}</dd>
                            </Fragment>
                          ))}
                        </dl>
                      </TableCell>
                    </TableRow>
                  ) : null}
                </Fragment>
              ))
            )}
          </TableBody>
        </Table>
      )}
    </div>
  );
}
```

Check: `appLabel(name)` returns a display name for an empty name; `formatPercent` takes a 0–100 number (see `lib/metrics.ts:178`). The `aria-label` on the search input plus `type="search"` gives role `searchbox`.

- [ ] **Step 4: Wire the tabs into the Monitoring page**

`web/src/pages/monitoring.tsx`:

Imports to add:

```tsx
import { ProcessesTab } from "@/components/monitoring/processes-tab";
import { TabsContent } from "@/components/ui/tabs";
import { UnderlineTabs } from "@/components/underline-tabs";
import { useTabParam } from "@/hooks/use-tab-param";
```

Module scope:

```tsx
const TABS = ["overview", "processes"] as const;
type Tab = (typeof TABS)[number];
```

In `MonitoringPage`, with the other hooks at the top (before the early `return` for no server — hooks must not be conditional):

```tsx
  const [tab, setTab] = useTabParam<Tab>(TABS, "overview");
```

Render: keep `PageHeader` (its `RangeMenu` and freshness only make sense for Overview — pass `actions={tab === "overview" ? <RangeMenu …/> : undefined}` and the `DataFreshness` subtitle part only when `tab === "overview"`; keep the server name in both). Keep `FiringBanner` above the tabs. Then wrap everything from `{error ? …}` through `<TargetAlertsCard …/>` like this:

```tsx
      <UnderlineTabs<Tab>
        value={tab}
        onValueChange={setTab}
        label="Monitoring views"
        items={[
          { value: "overview", label: "Overview" },
          { value: "processes", label: "Processes" },
        ]}
      >
        <TabsContent value="overview" className="mt-6 space-y-6">
          {/* the existing error line, KPI grid, charts, Apps section,
              More details disclosure and TargetAlertsCard, unchanged */}
        </TabsContent>
        <TabsContent value="processes" className="mt-6">
          {tab === "processes" ? <ProcessesTab serverId={activeServerId} /> : null}
        </TabsContent>
      </UnderlineTabs>
```

The overview's `useServerMetrics` / `useMetricsOverview` keep running as today (out of scope to change). Update the `MonitoringPage` doc comment to mention the two tabs.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/monitoring src/pages/monitoring.test.tsx && pnpm --dir web run lint && pnpm --dir web run build`
Expected: PASS; lint clean; build succeeds (runs `tsc -b`).

- [ ] **Step 6: Commit**

```bash
git add web/src/components/monitoring web/src/pages/monitoring.tsx web/src/pages/monitoring.test.tsx
git commit -m "feat(web): Processes tab on the Monitoring page

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Final verification (after all tasks)

```bash
go build ./... && go vet ./... && go test ./...
make test-pg   # if Docker is available
pnpm --dir web run lint && pnpm --dir web test && pnpm --dir web run build
bash scripts/install_test.sh
make openapi && git diff --exit-code api/openapi.yaml
```
