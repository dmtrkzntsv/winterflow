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
	seen bool // some container resolved once: the layout is known
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

// LayoutKnown reports whether container cgroups can be found on this host:
// true once any container resolved, otherwise when a Docker cgroup parent of
// a known layout exists (resolve's four places). False means container
// metrics are unavailable here, as opposed to one container's cgroup being
// gone this tick (replaced by a redeploy, or between crash-loop attempts).
func (c *Containers) LayoutKnown() bool {
	if c.seen {
		return true
	}
	for _, base := range [][]string{{}, {"memory"}} {
		if exists(c.cg(append(base, "docker")...)) {
			return true
		}
		if m, _ := filepath.Glob(c.cg(append(base, "system.slice", "docker-*.scope")...)); len(m) > 0 {
			return true
		}
	}
	return false
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
			c.seen = true
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
