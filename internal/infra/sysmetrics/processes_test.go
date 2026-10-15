package sysmetrics

import (
	"fmt"
	"maps"
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

// ioStat renders a cgroup v2 io.stat with one line per {rbytes, wbytes},
// on the whole disks sda (8:0) and sdb (8:16) of baseTree.
func ioStat(devs ...[2]uint64) string {
	var b strings.Builder
	for i, d := range devs {
		fmt.Fprintf(&b, "8:%d rbytes=%d wbytes=%d rios=1 wios=1 dbytes=0 dios=0\n", i*16, d[0], d[1])
	}
	return b.String()
}

const cid = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func baseTree(t *testing.T) string {
	root := tree(t, map[string]string{
		// a cgroup v2 host with two whole disks
		"sys/fs/cgroup/cgroup.controllers": "cpuset cpu io memory pids\n",
		"sys/block/sda/":                   "",
		"sys/block/sdb/":                   "",
		"proc/stat":                        "cpu  1 2 3 4 5 6 7 8\nbtime 1700000000\n",
		"proc/meminfo":                     "MemTotal: 1000 kB\nMemAvailable: 500 kB\n",
		"etc/passwd":                       "root:x:0:0:root:/root:/bin/sh\nalice:x:1000:1000::/home/alice:/bin/sh\n",
		// systemd-like root process
		"proc/1/stat":    statLine(1, "systemd", "S", 0, 100, 0, 1, 500),
		"proc/1/status":  "Name:\tsystemd\nUid:\t0\t0\t0\t0\nVmRSS:\t   100 kB\n",
		"proc/1/cmdline": "/sbin/init\x00splash\x00",
		"proc/1/cgroup":  "0::/init.scope\n",
		// kthreadd and a kernel worker
		"proc/2/stat":   statLine(2, "kthreadd", "S", 0, 0, 0, 1, 10),
		"proc/3/stat":   statLine(3, "kworker/0:1", "I", 2, 0, 0, 1, 11),
		"proc/3/cgroup": "0::/\n", // the root cgroup: no group
		// a container process owned by alice
		"proc/42/stat":    statLine(42, "nginx: worker", "R", 1, 10, 10, 4, 900),
		"proc/42/status":  "Name:\tnginx\nUid:\t1000\t1000\t1000\t1000\nVmRSS:\t   250 kB\n",
		"proc/42/cmdline": "nginx: worker process\x00",
		"proc/42/cgroup":  "0::/system.slice/docker-" + cid + ".scope\n",
		"proc/self/":      "", // non-numeric entries are skipped
	})
	link(t, root, map[string]string{
		"sys/dev/block/8:0":  "../../devices/pci0000:00/0000:00:17.0/ata1/host0/target0:0:0/0:0:0:0/block/sda",
		"sys/dev/block/8:16": "../../devices/pci0000:00/0000:00:17.0/ata2/host1/target1:0:0/1:0:0:0/block/sdb",
	})
	return root
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
	if p := byPID(resp.Processes)[42]; p.CPUPct != nil {
		t.Fatalf("reused pid inherited its cpu: %+v", p)
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

func byGroup(list []command.ProcessGroup) map[string]command.ProcessGroup {
	m := map[string]command.ProcessGroup{}
	for _, g := range list {
		m[g.ID] = g
	}
	return m
}

const (
	scopeID = "/system.slice/docker-" + cid + ".scope"
	pgID    = "/system.slice/postgresql.service"
)

// groupTree is baseTree plus a service with two processes and two devices,
// a second container process in a sub-cgroup of the container, a process
// with only cgroup v1 lines and one whose cgroup path climbs out with "..".
func groupTree(t *testing.T) string {
	root := baseTree(t)
	write(t, root, map[string]string{
		"proc/43/stat":   statLine(43, "nginx: master", "S", 1, 0, 0, 1, 800),
		"proc/43/cgroup": "0::" + scopeID + "/sub\n",
		"proc/50/stat":   statLine(50, "postgres", "S", 1, 0, 0, 1, 600),
		"proc/50/cgroup": "0::" + pgID + "\n",
		"proc/51/stat":   statLine(51, "postgres", "S", 50, 0, 0, 1, 601),
		"proc/51/cgroup": "0::" + pgID + " (deleted)\n",
		"proc/60/stat":   statLine(60, "old", "S", 1, 0, 0, 1, 700),
		"proc/60/cgroup": "12:memory:/user.slice\n4:cpu,cpuacct:/user.slice\n",
		"proc/61/stat":   statLine(61, "odd", "S", 1, 0, 0, 1, 701),
		"proc/61/cgroup": "0::/system.slice/../../etc\n",

		"sys/fs/cgroup" + pgID + "/io.stat":        ioStat([2]uint64{1000, 1000}, [2]uint64{0, 5000}),
		"sys/fs/cgroup" + scopeID + "/io.stat":     ioStat([2]uint64{100, 200}),
		"sys/fs/cgroup" + scopeID + "/sub/io.stat": ioStat([2]uint64{9, 9}), // counted in the scope's own
		"sys/fs/cgroup/etc/io.stat":                ioStat([2]uint64{1, 1}),
		"sys/fs/cgroup/user.slice/io.stat":         ioStat([2]uint64{1, 1}),
		"sys/fs/cgroup/init.scope/":                "", // no io.stat: rates unknown
	})
	return root
}

func TestProcessesGroups(t *testing.T) {
	root := groupTree(t)
	c := &fakeClock{t: time.Unix(1700001000, 0)}
	c.between = func() {
		write(t, root, map[string]string{
			"sys/fs/cgroup" + pgID + "/io.stat":    ioStat([2]uint64{1500, 3000}, [2]uint64{0, 7000}), // +500 read, +4000 written in 1 s
			"sys/fs/cgroup" + scopeID + "/io.stat": ioStat([2]uint64{100, 1200}),
		})
	}
	p := newTestProcesses(root, c)
	reads := map[string]int{}
	p.ioStatRead = func(id string) { reads[id]++ }
	resp, err := p.List()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Total != 9 || len(resp.Groups) != 3 {
		t.Fatalf("total %d, groups %+v", resp.Total, resp.Groups)
	}
	g := byGroup(resp.Groups)
	pg := g[pgID]
	if pg.Kind != "service" || pg.Label != "postgresql.service" || pg.ContainerID != "" || pg.Processes != 2 {
		t.Fatalf("service group = %+v", pg)
	}
	if pg.ReadBps == nil || *pg.ReadBps != 500 || pg.WriteBps == nil || *pg.WriteBps != 4000 {
		t.Fatalf("service rates = %v/%v", pg.ReadBps, pg.WriteBps)
	}
	ct := g[scopeID]
	if ct.Kind != "container" || ct.Label != "" || ct.ContainerID != cid || ct.Processes != 2 {
		t.Fatalf("container group = %+v", ct)
	}
	if ct.ReadBps == nil || *ct.ReadBps != 0 || ct.WriteBps == nil || *ct.WriteBps != 1000 {
		t.Fatalf("container rates = %v/%v", ct.ReadBps, ct.WriteBps)
	}
	in := g["/init.scope"]
	if in.Kind != "scope" || in.Label != "init.scope" || in.Processes != 1 || in.ReadBps != nil || in.WriteBps != nil {
		t.Fatalf("group without io.stat = %+v", in)
	}
	// Busiest writer first; unknown rates last.
	if resp.Groups[0].ID != pgID || resp.Groups[1].ID != scopeID || resp.Groups[2].ID != "/init.scope" {
		t.Fatalf("group order = %+v", resp.Groups)
	}
	procs := byPID(resp.Processes)
	for pid, want := range map[int]string{1: "/init.scope", 2: "", 3: "", 42: scopeID, 43: scopeID, 50: pgID, 51: pgID, 60: "", 61: ""} {
		if got := procs[pid].Group; got != want {
			t.Errorf("pid %d group = %q, want %q", pid, got, want)
		}
	}
	// io.stat is read once per group and reading, never per process.
	for _, id := range []string{pgID, scopeID, "/init.scope"} {
		if reads[id] != 2 {
			t.Errorf("%s io.stat read %d times over two readings", id, reads[id])
		}
	}
	if len(reads) != 3 {
		t.Errorf("io.stat reads = %v", reads)
	}
}

func TestProcessesGroupCountersBackwards(t *testing.T) {
	root := groupTree(t)
	c := &fakeClock{t: time.Unix(1700001000, 0)}
	c.between = func() {
		write(t, root, map[string]string{"sys/fs/cgroup" + pgID + "/io.stat": ioStat([2]uint64{10, 10})})
		removeAll(t, root, "sys/fs/cgroup"+scopeID+"/io.stat")
	}
	resp, err := newTestProcesses(root, c).List()
	if err != nil {
		t.Fatal(err)
	}
	g := byGroup(resp.Groups)
	// sda went down and sdb left: neither device adds anything, and the
	// group, read both times, still has a rate.
	if pg := g[pgID]; pg.ReadBps == nil || *pg.ReadBps != 0 || pg.WriteBps == nil || *pg.WriteBps != 0 || pg.Processes != 2 {
		t.Fatalf("devices that went down or left: %s/%s, want 0/0 (%+v)", bps(pg.ReadBps), bps(pg.WriteBps), pg)
	}
	if ct := g[scopeID]; ct.ReadBps != nil || ct.WriteBps != nil || ct.Processes != 2 {
		t.Fatalf("an io.stat gone in the second reading gave a rate: %+v", ct)
	}
}

func TestProcessesGroupNewInSecondReading(t *testing.T) {
	root := groupTree(t)
	c := &fakeClock{t: time.Unix(1700001000, 0)}
	c.between = func() {
		write(t, root, map[string]string{
			"proc/70/stat":   statLine(70, "new", "S", 1, 0, 0, 1, 990),
			"proc/70/cgroup": "0::/system.slice/new.service\n",
			"sys/fs/cgroup/system.slice/new.service/io.stat": ioStat([2]uint64{5, 5}),
		})
	}
	resp, err := newTestProcesses(root, c).List()
	if err != nil {
		t.Fatal(err)
	}
	g, ok := byGroup(resp.Groups)["/system.slice/new.service"]
	if !ok || g.Processes != 1 || g.ReadBps != nil || g.WriteBps != nil {
		t.Fatalf("new group = %+v (listed %v)", g, ok)
	}
}

func TestSortGroups(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	groups := []command.ProcessGroup{
		{ID: "/nil-b"},
		{ID: "/w100-r0", WriteBps: f(100), ReadBps: f(0)},
		{ID: "/w100-r50-b", WriteBps: f(100), ReadBps: f(50)},
		{ID: "/nil-a"},
		{ID: "/w500", WriteBps: f(500), ReadBps: f(0)},
		{ID: "/w100-r50-a", WriteBps: f(100), ReadBps: f(50)},
		{ID: "/w0-r9", WriteBps: f(0), ReadBps: f(9)},
		// equal rates: containers first, then more processes, then the id
		{ID: "/z-ctr", Kind: "container", ContainerID: cid, WriteBps: f(0), ReadBps: f(0), Processes: 1},
		{ID: "/a-svc-3", Kind: "service", WriteBps: f(0), ReadBps: f(0), Processes: 3},
		{ID: "/b-svc-3", Kind: "service", WriteBps: f(0), ReadBps: f(0), Processes: 3},
		{ID: "/c-svc-9", Kind: "service", WriteBps: f(0), ReadBps: f(0), Processes: 9},
		{ID: "/nil-ctr", Kind: "container", ContainerID: cid},
		{ID: "/nil-5", Kind: "scope", Processes: 5},
	}
	sortGroups(groups)
	var got []string
	for _, g := range groups {
		got = append(got, g.ID)
	}
	want := "/w500 /w100-r50-a /w100-r50-b /w100-r0 /w0-r9 /z-ctr /c-svc-9 /a-svc-3 /b-svc-3 /nil-ctr /nil-5 /nil-a /nil-b"
	if strings.Join(got, " ") != want {
		t.Fatalf("order = %v, want %s", got, want)
	}
}

func TestGroupFromCgroup(t *testing.T) {
	for in, want := range map[string][2]string{
		"0::" + scopeID + "\n":                             {scopeID, cid},
		"0::" + scopeID + "/sub/deeper\n":                  {scopeID, cid},
		"0::/docker/" + cid + "\n":                         {"/docker/" + cid, cid},
		"0::/docker/" + cid + "/init\n":                    {"/docker/" + cid, cid},
		"0::/system.slice/foo.service (deleted)\n":         {"/system.slice/foo.service", ""},
		"1:name=systemd:/init.scope\n0::/init.scope\n":     {"/init.scope", ""},
		"0::/system.slice//x.service/\n":                   {"/system.slice/x.service", ""},
		"0::/user.slice/user-1000.slice/session-2.scope\n": {"/user.slice/user-1000.slice/session-2.scope", ""},
		"0::/\n":                          {"", ""},
		"0::\n":                           {"", ""},
		"12:memory:/docker/" + cid + "\n": {"", ""},
		"0::/system.slice/../../etc\n":    {"", ""},
		"0::/a/..\n":                      {"", ""},
		"0::system.slice/x.service\n":     {"", ""},
	} {
		id, c := groupFromCgroup([]byte(in))
		if id != want[0] || c != want[1] {
			t.Errorf("%q: got %q %q, want %q %q", in, id, c, want[0], want[1])
		}
	}
}

func TestGroupKind(t *testing.T) {
	for id, want := range map[string][2]string{
		"/system.slice/postgresql.service":            {"service", "postgresql.service"},
		"/user.slice/user-1000.slice/session-3.scope": {"scope", "session-3.scope"},
		"/user.slice/user-1000.slice":                 {"slice", "user-1000.slice"},
		"/kubepods/burstable":                         {"other", "burstable"},
	} {
		if kind, label := groupKind(id, ""); kind != want[0] || label != want[1] {
			t.Errorf("%s: got %s %q, want %s %q", id, kind, label, want[0], want[1])
		}
	}
	if kind, label := groupKind(scopeID, cid); kind != "container" || label != "" {
		t.Errorf("container: got %s %q", kind, label)
	}
}

func TestParseIOStat(t *testing.T) {
	all := func(string) bool { return true }
	got := parseIOStat([]byte("8:0 rbytes=10 wbytes=20 rios=1 wios=2 dbytes=3 dios=4\n259:0 rbytes=5 wbytes=7 rios=1 wios=1 dbytes=0 dios=0\n8:16 rbytes=x wbytes\n"), all)
	want := map[string]devCounters{"8:0": {10, 20}, "259:0": {5, 7}, "8:16": {}}
	if !maps.Equal(got, want) {
		t.Fatalf("parsed %v, want %v", got, want)
	}
	if got := parseIOStat(nil, all); len(got) != 0 {
		t.Fatalf("empty io.stat = %v", got)
	}
	var asked []string
	only259 := func(dev string) bool { asked = append(asked, dev); return dev == "259:0" }
	if got := parseIOStat([]byte("8:0 rbytes=10 wbytes=20\n259:0 rbytes=5 wbytes=7\n\n"), only259); !maps.Equal(got, map[string]devCounters{"259:0": {5, 7}}) {
		t.Fatalf("kept 259:0 only: %v", got)
	}
	if strings.Join(asked, " ") != "8:0 259:0" {
		t.Fatalf("devices asked = %v", asked)
	}
}

// diskStat renders an io.stat line per device (MAJ:MIN -> {rbytes, wbytes}).
func diskStat(devs map[string][2]uint64) string {
	var b strings.Builder
	for dev, d := range devs {
		fmt.Fprintf(&b, "%s rbytes=%d wbytes=%d rios=1 wios=1 dbytes=0 dios=0\n", dev, d[0], d[1])
	}
	return b.String()
}

// A group's disk I/O counts whole physical disks only: zram swap, loop
// files and device-mapper stacks (LVM, dm-crypt) on top of a disk are not
// counted, as on the host Disk I/O chart.
func TestProcessesGroupsCountOnlyWholeDisks(t *testing.T) {
	root := baseTree(t)
	write(t, root, map[string]string{
		"sys/block/nvme0n1/": "", "sys/block/zram0/": "", "sys/block/loop0/": "", "sys/block/dm-0/": "",
		"sys/fs/cgroup" + scopeID + "/io.stat": diskStat(map[string][2]uint64{
			"259:0": {1000, 1000}, "251:0": {1000, 1000}, "7:0": {1000, 1000}, "253:0": {1000, 1000}, "259:1": {1000, 1000},
		}),
	})
	link(t, root, map[string]string{
		"sys/dev/block/259:0": "../../devices/pci0000:00/0000:00:1d.0/0000:02:00.0/nvme/nvme0/nvme0n1",
		"sys/dev/block/259:1": "../../devices/pci0000:00/0000:00:1d.0/0000:02:00.0/nvme/nvme0/nvme0n1/nvme0n1p1",
		"sys/dev/block/251:0": "../../devices/virtual/block/zram0",
		"sys/dev/block/7:0":   "../../devices/virtual/block/loop0",
		"sys/dev/block/253:0": "../../devices/virtual/block/dm-0",
	})
	grow := func(n uint64) {
		write(t, root, map[string]string{"sys/fs/cgroup" + scopeID + "/io.stat": diskStat(map[string][2]uint64{
			"259:0": {1000 + n, 1000 + 2*n}, "251:0": {9000 * n, 9000 * n}, "7:0": {7000 * n, 7000 * n},
			"253:0": {5000 * n, 5000 * n}, "259:1": {3000 * n, 3000 * n},
		})})
	}
	c := &fakeClock{t: time.Unix(1700001000, 0)}
	c.between = func() { grow(100) }
	p := newTestProcesses(root, c)
	resp, err := p.List()
	if err != nil {
		t.Fatal(err)
	}
	ct := byGroup(resp.Groups)[scopeID]
	if ct.ReadBps == nil || *ct.ReadBps != 100 || ct.WriteBps == nil || *ct.WriteBps != 200 {
		t.Fatalf("rates = %v/%v, want nvme0n1's 100/200", ct.ReadBps, ct.WriteBps)
	}
	// The verdict is kept per device: with the sysfs links gone the next
	// reading still counts nvme0n1 and still skips the others.
	removeAll(t, root, "sys/dev/block")
	c.t = c.t.Add(time.Second)
	grow(200)
	resp, err = p.List()
	if err != nil {
		t.Fatal(err)
	}
	ct = byGroup(resp.Groups)[scopeID]
	if ct.ReadBps == nil || *ct.ReadBps != 100 || ct.WriteBps == nil || *ct.WriteBps != 200 {
		t.Fatalf("cached rates = %v/%v, want 100/200", ct.ReadBps, ct.WriteBps)
	}
}

// A device whose sysfs link cannot be read, or whose /sys/block entry is
// missing, is not counted, and neither is an io.stat key that is no
// MAJ:MIN. Such a verdict is not kept: once the device's link and entry
// appear (hot-plug, a transient failure) the next reading counts it.
func TestProcessesGroupsUnknownDevice(t *testing.T) {
	root := baseTree(t)
	// sdc (8:32) has no link yet; sdd (8:48) has a link but no /sys/block entry.
	link(t, root, map[string]string{"sys/dev/block/8:48": "../../devices/pci0000:00/ata4/host3/target3:0:0/3:0:0:0/block/sdd"})
	n := uint64(0)
	grow := func() {
		n++
		write(t, root, map[string]string{"sys/fs/cgroup" + scopeID + "/io.stat": fmt.Sprintf(
			"8:32 rbytes=%d wbytes=%d\n8:48 rbytes=%d wbytes=%d\n../../x rbytes=%d wbytes=%d\n8:0 rbytes=%d wbytes=%d\n",
			100*n, 100*n, 1000*n, 1000*n, 50*n, 50*n, n, 2*n)})
	}
	grow()
	c := &fakeClock{t: time.Unix(1700001000, 0)}
	c.between = grow
	p := newTestProcesses(root, c)
	resp, err := p.List()
	if err != nil {
		t.Fatal(err)
	}
	ct := byGroup(resp.Groups)[scopeID]
	if ct.ReadBps == nil || *ct.ReadBps != 1 || ct.WriteBps == nil || *ct.WriteBps != 2 {
		t.Fatalf("rates = %v/%v, want sda's 1/2", ct.ReadBps, ct.WriteBps)
	}
	write(t, root, map[string]string{"sys/block/sdc/": "", "sys/block/sdd/": ""})
	link(t, root, map[string]string{"sys/dev/block/8:32": "../../devices/pci0000:00/ata3/host2/target2:0:0/2:0:0:0/block/sdc"})
	// The reading that first counts sdc and sdd has no previous counters
	// for them: only sda's delta, no jump by their totals. The one after
	// it has their rates.
	for i, want := range [][2]float64{{1, 2}, {1101, 1102}} {
		c.t = c.t.Add(time.Second)
		grow()
		if resp, err = p.List(); err != nil {
			t.Fatal(err)
		}
		ct = byGroup(resp.Groups)[scopeID]
		if ct.ReadBps == nil || *ct.ReadBps != want[0] || ct.WriteBps == nil || *ct.WriteBps != want[1] {
			t.Fatalf("reading %d: rates = %s/%s, want %v", i+1, bps(ct.ReadBps), bps(ct.WriteBps), want)
		}
	}
}

// A device that leaves a group's io.stat between readings adds nothing:
// the rate is the other devices' delta, never negative or nil.
func TestProcessesGroupDeviceLeaves(t *testing.T) {
	root := baseTree(t)
	write(t, root, map[string]string{"sys/fs/cgroup" + scopeID + "/io.stat": ioStat([2]uint64{100, 100}, [2]uint64{9000, 9000})})
	c := &fakeClock{t: time.Unix(1700001000, 0)}
	c.between = func() {
		write(t, root, map[string]string{"sys/fs/cgroup" + scopeID + "/io.stat": ioStat([2]uint64{130, 170})})
	}
	resp, err := newTestProcesses(root, c).List()
	if err != nil {
		t.Fatal(err)
	}
	ct := byGroup(resp.Groups)[scopeID]
	if ct.ReadBps == nil || *ct.ReadBps != 30 || ct.WriteBps == nil || *ct.WriteBps != 70 {
		t.Fatalf("rates = %s/%s, want sda's 30/70", bps(ct.ReadBps), bps(ct.WriteBps))
	}
}

func bps(v *float64) string {
	if v == nil {
		return "nil"
	}
	return fmt.Sprint(*v)
}

// Without cgroup.controllers at the cgroup root (cgroup v1, or hybrid with
// the unified tree under /sys/fs/cgroup/unified) the "0::" paths do not
// name files under /sys/fs/cgroup: no groups at all.
func TestProcessesHybridCgroupHasNoGroups(t *testing.T) {
	root := groupTree(t)
	removeAll(t, root, "sys/fs/cgroup/cgroup.controllers")
	write(t, root, map[string]string{"proc/80/stat": statLine(80, "x", "S", 1, 0, 0, 1, 990), "proc/80/cgroup": "0::/system.slice/x.service\n"})
	c := &fakeClock{t: time.Unix(1700001000, 0)}
	p := newTestProcesses(root, c)
	p.ioStatRead = func(id string) { t.Errorf("io.stat read for %s", id) }
	resp, err := p.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Groups) != 0 {
		t.Fatalf("groups = %+v", resp.Groups)
	}
	for _, proc := range resp.Processes {
		if proc.Group != "" {
			t.Errorf("pid %d group = %q", proc.PID, proc.Group)
		}
	}
	if byPID(resp.Processes)[42].ContainerID != cid {
		t.Fatal("container id lost")
	}
}

func TestProcessesCapAndCmdline(t *testing.T) {
	files := map[string]string{
		"proc/stat":                        "cpu  1 2 3 4 5 6 7 8\nbtime 1700000000\n",
		"proc/meminfo":                     "MemTotal: 1000 kB\n",
		"sys/fs/cgroup/cgroup.controllers": "io\n",
	}
	for pid := 10; pid < 10+command.MaxProcesses+5; pid++ {
		files[fmt.Sprintf("proc/%d/stat", pid)] = statLine(pid, "p", "S", 1, uint64(pid), 0, 1, 1)
		files[fmt.Sprintf("proc/%d/cgroup", pid)] = fmt.Sprintf("0::/system.slice/s%d.service\n", pid%3)
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
	// Groups cover every process seen, the ones past the cap too.
	inGroups := 0
	for _, g := range resp.Groups {
		inGroups += g.Processes
	}
	if len(resp.Groups) != 3 || inGroups != resp.Total {
		t.Fatalf("%d groups hold %d processes, want 3 holding %d", len(resp.Groups), inGroups, resp.Total)
	}
	found, maxPID := false, 0
	for _, p := range resp.Processes {
		if p.PID == 10 {
			found = true
			if len(p.Cmdline) != 512 {
				t.Fatalf("cmdline len %d", len(p.Cmdline))
			}
		}
		maxPID = max(maxPID, p.PID)
	}
	if !found {
		t.Fatal("pid 10 not listed")
	}
	// Equal CPU and RSS: the highest pids are the ones dropped.
	if want := 10 + command.MaxProcesses - 1; maxPID != want {
		t.Fatalf("max listed pid %d, want %d", maxPID, want)
	}
}

func TestRedactCmdline(t *testing.T) {
	nul := func(args ...string) []byte { return []byte(strings.Join(args, "\x00") + "\x00") }
	for name, tc := range map[string]struct {
		in   []byte
		want string
	}{
		"flag then value":   {nul("redis-server", "--requirepass", "s3cret"), "redis-server --requirepass ***"},
		"flag=value":        {nul("app", "--password=hunter2"), "app --password=***"},
		"env style":         {nul("env", "POSTGRES_PASSWORD=x", "psql"), "env POSTGRES_PASSWORD=*** psql"},
		"upper-case name":   {nul("app", "--API_KEY=abc"), "app --API_KEY=***"},
		"no keyword":        {nul("nginx", "-p", "8080"), "nginx -p 8080"},
		"api key then flag": {nul("app", "--api-key", "k", "--verbose"), "app --api-key *** --verbose"},
		"secret flag, flag": {nul("app", "--token", "--verbose"), "app --token --verbose"},
		"token is last":     {nul("app", "--token"), "app --token"},
		"url password":      {nul("app", "postgres://u:pw@db:5432/x"), "app postgres://u:***@db:5432/x"},
		"url in assignment": {nul("app", "DSN=postgres://u:pw@db/x"), "app DSN=postgres://u:***@db/x"},
		"url without pass":  {nul("app", "postgres://u@db:5432/x", "https://example.com:8443/a"), "app postgres://u@db:5432/x https://example.com:8443/a"},
		"plain arguments":   {nul("sh", "-c", "echo hi", "--name=web"), "sh -c echo hi --name=web"},
		"shell form":        {nul("sh", "-c", "redis-server --requirepass s3cret --port 6379"), "sh -c redis-server --requirepass *** --port 6379"},
		"mysql pwd":         {nul("env", "MYSQL_PWD=x", "mysql"), "env MYSQL_PWD=*** mysql"},
		"pwd flag":          {nul("app", "--pwd", "x"), "app --pwd ***"},
		"value with a space only first word is redacted": {nul("app", "--password=a b"), "app --password=*** b"},
		"single-quoted assignment":                       {nul("app", "'--password=x'"), "app '--password=***'"},
		"quoted flag":                                    {nul("app", `"--requirepass"`, "x"), `app "--requirepass" ***`},
		"quoted multi-word value":                        {nul("app", "--requirepass", `"a b c"`, "--port", "1"), "app --requirepass *** --port 1"},
		"quoted assignment value":                        {nul("app", `DB_PASSWORD="a b"`, "--port", "1"), "app DB_PASSWORD=*** --port 1"},
		"assignment inside quotes":                       {nul("app", "'--password=a b'", "x"), "app '--password=***' x"},
		"flag inside quotes":                             {nul("app", "'--requirepass", "a", "b'", "x"), "app '--requirepass ***' x"},
		"unterminated quote":                             {nul("app", "--token", `"a b`), "app --token ***"},
		"mysql attached":                                 {nul("mysql", "-uroot", "-phunter2", "db"), "mysql -uroot -p*** db"},
		"mysqldump path":                                 {nul("/usr/bin/mysqldump", "-psecret"), "/usr/bin/mysqldump -p***"},
		"mysql bare -p":                                  {nul("mysql", "-p", "db"), "mysql -p db"},
		"mariadb quoted":                                 {nul("mariadb", "-p'a b'", "db"), "mariadb -p*** db"},
		"mysql in shell form":                            {nul("sh", "-c", "mysql -pabc db"), "sh -c mysql -p*** db"},
		"-p without a mysql client":                      {nul("app", "-p8080"), "app -p8080"},
		"url password with @ and /":                      {nul("app", "redis://u:p@ss/x@host:6379"), "app redis://u:***@host:6379"},
		"url with only a password":                       {nul("app", "redis://:pw@host"), "app redis://:***@host"},
		"url with @ in path only":                        {nul("app", "https://example.com/a@b"), "app https://example.com/a@b"},
		"authorization header":                           {nul("curl", "-H", "Authorization: Bearer abc"), `curl -H Authorization: Bearer ***`},
		"quoted authorization":                           {nul("curl", "-H", `"Authorization: Bearer abc"`), `curl -H "Authorization: Bearer ***"`},
		"basic scheme, any case":                         {nul("curl", "-H", `'authorization: basic dXNlcjpwdw=='`, "http://x"), `curl -H 'authorization: basic ***' http://x`},
		"header without a scheme":                        {nul("curl", "-H", "Authorization: abc"), "curl -H Authorization: ***"},
		"x-api-key header":                               {nul("curl", "-H", "x-api-key: k"), "curl -H x-api-key: ***"},
		"quoted multi-word header":                       {nul("curl", "-H", `"Authorization: Bearer a b"`, "u"), `curl -H "Authorization: Bearer ***" u`},
		"scheme closes the quote":                        {nul("curl", "-H", `"Authorization: Bearer"`, "u"), `curl -H "Authorization: Bearer" u`},
		"header name, nothing more":                      {nul("curl", "Authorization:"), "curl Authorization:"},
		"header without keyword":                         {nul("curl", "-H", "Accept: json"), "curl -H Accept: json"},
		"redis-cli -a":                                   {nul("redis-cli", "-a", "s3cret", "ping"), "redis-cli -a *** ping"},
		"redis-cli -a attached":                          {nul("redis-cli", "-as3cret", "ping"), "redis-cli -a*** ping"},
		"redis-cli --pass":                               {nul("redis-cli", "--pass", "s3cret", "ping"), "redis-cli --pass *** ping"},
		"keydb-cli path":                                 {nul("/usr/local/bin/keydb-cli", "-a", "x", "ping"), "/usr/local/bin/keydb-cli -a *** ping"},
		"valkey-cli after other options":                 {nul("valkey-cli", "-h", "cache", "-a", "x", "ping"), "valkey-cli -h cache -a *** ping"},
		"redis-cli in shell form":                        {nul("sh", "-c", "redis-cli -a s3cret ping"), "sh -c redis-cli -a *** ping"},
		"redis-cli quoted password":                      {nul("sh", "-c", `redis-cli -a "s3 cret" ping`), "sh -c redis-cli -a *** ping"},
		"redis-cli without a password":                   {nul("redis-cli", "-h", "host", "ping"), "redis-cli -h host ping"},
		"-a without a redis client":                      {nul("app", "-a", "value"), "app -a value"},
		"sshpass -p, then ssh's port":                    {nul("sshpass", "-p", "pw", "ssh", "-p", "2222", "host"), "sshpass -p *** ssh -p 2222 host"},
		"sshpass -p attached":                            {nul("sshpass", "-ppw", "ssh", "host"), "sshpass -p*** ssh host"},
		"sshpass from a file":                            {nul("sshpass", "-f", "/run/pw", "ssh", "host"), "sshpass -f /run/pw ssh host"},
		"mongosh -p":                                     {nul("mongosh", "-u", "admin", "-p", "pw", "--quiet"), "mongosh -u admin -p *** --quiet"},
		"mongo --password":                               {nul("mongo", "--password", "pw"), "mongo --password ***"},
		"mongosh bare -p prompts":                        {nul("mongosh", "-u", "admin", "-p"), "mongosh -u admin -p"},
		"mongosh in shell form":                          {nul("sh", "-c", "mongosh -u root -p pw --eval ping"), "sh -c mongosh -u root -p *** --eval ping"},
		"curl -u user:pass":                              {nul("curl", "-u", "admin:pw", "http://x"), "curl -u admin:*** http://x"},
		"curl --user, colon in password":                 {nul("curl", "--user", "admin:p:w", "http://x"), "curl --user admin:*** http://x"},
		"curl --user=":                                   {nul("curl", "--user=admin:pw", "http://x"), "curl --user=admin:*** http://x"},
		"curl -u attached":                               {nul("curl", "-uadmin:pw", "http://x"), "curl -uadmin:*** http://x"},
		"curl --proxy-user":                              {nul("curl", "-U", "p:x", "http://x"), "curl -U p:*** http://x"},
		"curl -u in shell form, quoted":                  {nul("sh", "-c", "curl -fsS -u 'admin:p w' http://x"), "sh -c curl -fsS -u 'admin:***' http://x"},
		"curl --user= quoted":                            {nul("sh", "-c", "curl --user='admin:p w' http://x"), "sh -c curl --user='admin:***' http://x"},
		"curl -u without a colon prompts":                {nul("curl", "-u", "admin", "http://x"), "curl -u admin http://x"},
		"wget --user without a colon":                    {nul("wget", "--user=admin", "--password=pw", "http://x"), "wget --user=admin --password=*** http://x"},
		"-u without curl":                                {nul("docker", "run", "-u", "1000:1000", "img"), "docker run -u 1000:1000 img"},
		"wget --header=, value follows":                  {nul("wget", "--header=Authorization: Bearer abc", "http://x"), "wget --header=Authorization: Bearer *** http://x"},
		"wget --header= attached value":                  {nul("wget", "--header=X-Api-Key:abc", "http://x"), "wget --header=X-Api-Key:*** http://x"},
		"wget --header= attached scheme":                 {nul("wget", "--header=Authorization:Bearer abc", "http://x"), "wget --header=Authorization:Bearer *** http://x"},
		"wget --header= quoted in shell form":            {nul("sh", "-c", "wget -q --header='X-Api-Key: abc' -O- http://x"), "sh -c wget -q --header='X-Api-Key: ***' -O- http://x"},
		"wget --header= without keyword":                 {nul("wget", "--header=Accept: json", "http://x"), "wget --header=Accept: json http://x"},
		"curl -H attached scheme":                        {nul("curl", "-H", "Authorization:Bearer abc", "http://x"), "curl -H Authorization:Bearer *** http://x"},
		"curl -H attached value":                         {nul("curl", "-H", "X-Api-Key:abc", "http://x"), "curl -H X-Api-Key:*** http://x"},
		"curl --header attached value":                   {nul("curl", "--header", "X-Auth-Token:abc"), "curl --header X-Auth-Token:***"},
		"curl -H attached, quoted":                       {nul("sh", "-c", "curl -H 'X-Api-Key:a b' http://x"), "sh -c curl -H 'X-Api-Key:***' http://x"},
		"attached scheme closes the quote":               {nul("sh", "-c", `curl -H "Authorization:Bearer" http://x`), `sh -c curl -H "Authorization:Bearer" http://x`},
		"attached scheme, nothing more":                  {nul("curl", "-H", "Authorization:Bearer"), "curl -H Authorization:Bearer"},
		"curl -H attached without keyword":               {nul("curl", "-H", "Accept:json"), "curl -H Accept:json"},
		"host:port outside a header":                     {nul("app", "--upstream", "keycloak:8080", "auth-service:9000"), "app --upstream keycloak:8080 auth-service:9000"},
		"empty":                                          {nil, ""},
		"redacted before cap":                            {append(nul(strings.Repeat("x", 600)), nul("--password=p")...), strings.Repeat("x", 512)},
	} {
		if got := redactCmdline(tc.in); got != tc.want {
			t.Errorf("%s: got %q want %q", name, got, tc.want)
		}
	}
}

func TestProcessesRedactsCmdline(t *testing.T) {
	root := baseTree(t)
	write(t, root, map[string]string{
		"proc/42/cmdline": "redis-server\x00--requirepass\x00s3cret\x00--dsn=postgres://u:pw@db/x\x00",
	})
	resp, err := newTestProcesses(root, &fakeClock{t: time.Unix(1700001000, 0)}).List()
	if err != nil {
		t.Fatal(err)
	}
	want := "redis-server --requirepass *** --dsn=postgres://u:***@db/x"
	if got := byPID(resp.Processes)[42].Cmdline; got != want {
		t.Fatalf("cmdline = %q, want %q", got, want)
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
		"0::/system.slice/docker-" + cid + ".scope\n":         cid,
		"0::/docker/" + cid + "\n":                            cid,
		"12:memory:/docker/" + cid + "\n4:cpu:/docker/" + cid: cid,
		"0::/user.slice/user-1000.slice/session-2.scope\n":    "",
		"0::/system.slice/containerd.service\n":               "",
	} {
		if got := containerIDFromCgroup([]byte(in)); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
