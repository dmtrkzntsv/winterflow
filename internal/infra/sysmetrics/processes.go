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
	comm, state   string
	ppid, threads int
	ticks, start  uint64
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
