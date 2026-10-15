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
		"empty":               {nil, ""},
		"redacted before cap": {append(nul(strings.Repeat("x", 600)), nul("--password=p")...), strings.Repeat("x", 512)},
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
