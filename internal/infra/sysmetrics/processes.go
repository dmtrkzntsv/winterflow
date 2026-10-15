package sysmetrics

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path"
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
	// cmdlineRead bounds the bytes read from /proc/<pid>/cmdline. It exceeds
	// cmdlineCap because redaction needs the arguments the cap would cut.
	cmdlineRead = 4096
	// prevMaxAge: an older previous reading is not used for rates (nobody
	// watched for a while); the call takes two readings instead.
	prevMaxAge = 60 * time.Second
	// firstGap separates the two readings of a call without a usable
	// previous one, so the first answer already has rates.
	firstGap = time.Second
)

// Processes reads every process under root/proc, and the disk I/O of
// their cgroups under root/sys/fs/cgroup. It keeps the previous reading per
// pid and per group, so a call's CPU % and disk rates cover the time since
// the call before. File reads only.
type Processes struct {
	root  string
	ncpu  int
	now   func() time.Time
	sleep func(time.Duration)

	mu         sync.Mutex
	prev       map[int]procCounters
	prevGroups map[string]groupCounters
	prevAt     time.Time
	users      map[int]string
	userAt     time.Time // /etc/passwd mtime users was read at

	ioStatRead func(id string) // test hook: a group's io.stat is read
}

// NewProcesses reads under root ("/" in production).
func NewProcesses(root string, ncpu int) *Processes {
	return &Processes{root: root, ncpu: ncpu, now: time.Now, sleep: time.Sleep}
}

// procCounters is what the CPU rate is computed from. start (starttime
// ticks) tells a reused pid apart.
type procCounters struct {
	start, ticks uint64
}

type procReading struct {
	p command.Process
	c procCounters
}

// groupCounters is a group's io.stat bytes, summed over its devices. ok is
// false when io.stat was not readable (the io controller is off there, or
// the cgroup went away).
type groupCounters struct {
	read, write uint64
	ok          bool
}

type groupReading struct {
	containerID string
	c           groupCounters
}

// reading is one pass over /proc and the cgroups its processes are in.
type reading struct {
	procs  map[int]procReading
	groups map[string]groupReading
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
	out := make([]command.Process, 0, len(cur.procs))
	members := make(map[string]int, len(cur.groups))
	for pid, r := range cur.procs {
		proc := r.p
		if prev, ok := p.prev[pid]; ok && prev.start == r.c.start && dt > 0 && r.c.ticks >= prev.ticks {
			v := float64(r.c.ticks-prev.ticks) / clkTck / dt * 100
			proc.CPUPct = &v
		}
		if memTotal > 0 {
			proc.MemPct = float64(proc.RSS) / float64(memTotal) * 100
		}
		if proc.Group != "" {
			members[proc.Group]++
		}
		out = append(out, proc)
	}
	groups := make([]command.ProcessGroup, 0, len(cur.groups))
	for id, r := range cur.groups {
		kind, label := groupKind(id, r.containerID)
		g := command.ProcessGroup{
			ID: id, Label: label, Kind: kind, ContainerID: r.containerID, Processes: members[id],
		}
		if prev, ok := p.prevGroups[id]; ok && prev.ok && r.c.ok && dt > 0 &&
			r.c.read >= prev.read && r.c.write >= prev.write {
			rd := float64(r.c.read-prev.read) / dt
			wr := float64(r.c.write-prev.write) / dt
			g.ReadBps, g.WriteBps = &rd, &wr
		}
		groups = append(groups, g)
	}
	sortGroups(groups)
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
		SampledAt: now.UnixMilli(), NCPU: p.ncpu, MemTotal: memTotal, Total: total, Processes: out, Groups: groups,
	}, nil
}

func cpuOf(p command.Process) float64 {
	if p.CPUPct == nil {
		return -1
	}
	return *p.CPUPct
}

// sortGroups orders groups busiest writer first, then reader; unknown
// rates sort below every known one, and the ID breaks ties.
func sortGroups(groups []command.ProcessGroup) {
	rate := func(v *float64) float64 {
		if v == nil {
			return -1
		}
		return *v
	}
	sort.Slice(groups, func(i, j int) bool {
		if wi, wj := rate(groups[i].WriteBps), rate(groups[j].WriteBps); wi != wj {
			return wi > wj
		}
		if ri, rj := rate(groups[i].ReadBps), rate(groups[j].ReadBps); ri != rj {
			return ri > rj
		}
		return groups[i].ID < groups[j].ID
	})
}

func (p *Processes) remember(r reading, at time.Time) {
	p.prev = make(map[int]procCounters, len(r.procs))
	for pid, x := range r.procs {
		p.prev[pid] = x.c
	}
	p.prevGroups = make(map[string]groupCounters, len(r.groups))
	for id, g := range r.groups {
		p.prevGroups[id] = g.c
	}
	p.prevAt = at
}

// read takes one reading of every process, then of each distinct group's
// io.stat (once per group, not per process). A pid that vanishes while it
// is read is skipped.
func (p *Processes) read() (reading, error) {
	stat, err := os.ReadFile(p.path("proc", "stat"))
	if err != nil {
		return reading{}, err
	}
	btime := parseBtime(stat)
	entries, err := os.ReadDir(p.path("proc"))
	if err != nil {
		return reading{}, err
	}
	out := reading{procs: make(map[int]procReading, len(entries)), groups: map[string]groupReading{}}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		r, cont, ok := p.readPID(pid, btime)
		if !ok {
			continue
		}
		out.procs[pid] = r
		if id := r.p.Group; id != "" {
			if _, seen := out.groups[id]; !seen {
				out.groups[id] = groupReading{containerID: cont, c: p.readIOStat(id)}
			}
		}
	}
	return out, nil
}

// readIOStat reads a group's io.stat. id is a clean absolute cgroup path
// (groupFromCgroup), so the file stays under root/sys/fs/cgroup.
func (p *Processes) readIOStat(id string) groupCounters {
	if p.ioStatRead != nil {
		p.ioStatRead(id)
	}
	b, err := os.ReadFile(p.path("sys", "fs", "cgroup", id, "io.stat"))
	if err != nil {
		return groupCounters{}
	}
	r, w := parseIOStat(b)
	return groupCounters{read: r, write: w, ok: true}
}

// readPID reads one process; groupCID is the container id of its group
// (empty when the group is no container's).
func (p *Processes) readPID(pid int, btime int64) (r procReading, groupCID string, ok bool) {
	dir := strconv.Itoa(pid)
	b, err := os.ReadFile(p.path("proc", dir, "stat"))
	if err != nil {
		return procReading{}, "", false
	}
	s, err := parsePIDStat(b)
	if err != nil {
		return procReading{}, "", false
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
	if b, err := readHead(p.path("proc", dir, "cmdline"), cmdlineRead); err == nil {
		proc.Cmdline = redactCmdline(b)
	}
	if b, err := os.ReadFile(p.path("proc", dir, "cgroup")); err == nil {
		proc.ContainerID = containerIDFromCgroup(b)
		proc.Group, groupCID = groupFromCgroup(b)
	}
	return procReading{p: proc, c: c}, groupCID, true
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

// readHead reads at most n bytes of a file, so a huge argv is never read
// in full.
func readHead(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, n))
}

var (
	secretWords = []string{"pass", "pwd", "secret", "token", "key", "auth", "cred"}
	// Patterns match a token with its shell quotes stripped from the front.
	// assignArg is "NAME=value" with optional leading dashes (-x=1, --x=1,
	// X=1); flagArg is a bare "-x" / "--name"; headerArg is an HTTP header
	// name ("Authorization:"), maybe after wget's "--header=", up to its ':'.
	assignArg = regexp.MustCompile(`^["']*-{0,2}([A-Za-z0-9_.-]+)=`)
	flagArg   = regexp.MustCompile(`^["']*--?[A-Za-z0-9_.-]+["']*$`)
	headerArg = regexp.MustCompile(`^["']*(?:--header=["']*)?([A-Za-z0-9_-]+):`)
	// urlScheme finds the start of URL userinfo (scheme://).
	urlScheme = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://`)

	// passOpts are the short options that carry a password, by the client
	// that reads them (named anywhere earlier in the command line, so the
	// shell form "sh -c 'redis-cli -a x'" counts too).
	passOpts = map[string]passOpt{
		// A bare -p prompts instead.
		"mysql": mysqlPass, "mysqldump": mysqlPass, "mysqladmin": mysqlPass,
		"mariadb": mysqlPass, "mariadb-dump": mysqlPass, "mysqlsh": mysqlPass,
		"redis-cli": redisPass, "keydb-cli": redisPass, "valkey-cli": redisPass, "redis-benchmark": redisPass,
		// sshpass takes one -p, before the command it runs (ssh -p 22).
		"sshpass": {flag: "-p", attached: true, next: true, once: true},
		"mongo":   mongoPass, "mongosh": mongoPass, "mongodump": mongoPass, "mongorestore": mongoPass,
		"mongoexport": mongoPass, "mongoimport": mongoPass,
	}
	mysqlPass = passOpt{flag: "-p", attached: true}
	redisPass = passOpt{flag: "-a", attached: true, next: true}
	mongoPass = passOpt{flag: "-p", next: true}

	// userClients take user:password in -u/--user (and -U/--proxy-user);
	// a value without ':' makes curl prompt.
	userClients = map[string]bool{"curl": true, "wget": true}
	userFlags   = map[string]bool{"-u": true, "--user": true, "-U": true, "--proxy-user": true}
	headerFlags = map[string]bool{"-H": true, "--header": true}
	authSchemes = map[string]bool{"bearer": true, "basic": true, "token": true, "digest": true}
)

// passOpt is a client's password option: attached (-psecret) and/or as
// the next argument (-a secret).
type passOpt struct {
	flag           string
	attached, next bool
	once           bool // only the first is the client's
}

func secretName(name string) bool {
	name = strings.ToLower(name)
	for _, w := range secretWords {
		if strings.Contains(name, w) {
			return true
		}
	}
	return false
}

func isQuote(c byte) bool { return c == '"' || c == '\'' }

// openWrap returns the quote that opens tok and is not closed inside it
// ("\"Authorization:" or `'--password=a`): the quoted text goes on into the
// following tokens. 0 when there is none.
func openWrap(tok string) byte {
	if tok != "" && isQuote(tok[0]) && strings.Count(tok, tok[:1])%2 == 1 {
		return tok[0]
	}
	return 0
}

// openQuote returns the first quote in tok when tok does not close it
// ("--header='Authorization:"), 0 otherwise.
func openQuote(tok string) byte {
	if k := strings.IndexAny(tok, `"'`); k >= 0 && strings.Count(tok, tok[k:k+1])%2 == 1 {
		return tok[k]
	}
	return 0
}

func trailingQuotes(tok string) string {
	return tok[len(strings.TrimRight(tok, `"'`)):]
}

// closeIdx is the first token from `from` that holds the quote q, that is
// the one that closes it. An unterminated quote swallows the rest.
func closeIdx(args []string, from int, q byte) int {
	for k := from; k < len(args); k++ {
		if strings.IndexByte(args[k], q) >= 0 {
			return k
		}
	}
	return len(args) - 1
}

// redactTail redacts the value in args[i] after its first prefixLen bytes
// (the name and "="). It returns the replacement and the last token used:
// a quoted value spans tokens up to the one that closes the quote, which
// may open in the prefix (--user='admin:p w').
func redactTail(args []string, i, prefixLen int) (string, int) {
	tok := args[i]
	prefix, rem := tok[:prefixLen], tok[prefixLen:]
	pq := strings.IndexAny(prefix, `"'`)
	if pq >= 0 && strings.Count(tok, prefix[pq:pq+1])%2 == 1 {
		return prefix + "***" + prefix[pq:pq+1], closeIdx(args, i+1, prefix[pq])
	}
	if rem != "" && isQuote(rem[0]) {
		if strings.Count(rem, rem[:1])%2 == 1 {
			return prefix + "***", closeIdx(args, i+1, rem[0])
		}
		if pq >= 0 {
			return prefix + "***" + prefix[pq:pq+1], i
		}
		return prefix + "***", i
	}
	if pq >= 0 {
		return prefix + "***" + trailingQuotes(rem), i
	}
	return prefix + "***", i
}

// redactNext redacts the value token args[j] that follows a secret name.
// wrap is the quote the name's token left open, if any: the value then runs
// to the token that closes it.
func redactNext(args []string, j int, wrap byte) (string, int) {
	if wrap != 0 {
		return "***" + string(wrap), closeIdx(args, j, wrap)
	}
	t := args[j]
	if isQuote(t[0]) && strings.Count(t, t[:1])%2 == 1 {
		return "***", closeIdx(args, j+1, t[0])
	}
	if isQuote(t[0]) {
		return "***", j
	}
	return "***" + trailingQuotes(t), j
}

// canRedactNext says whether args[j] is a value: it exists and is not
// itself an option.
func canRedactNext(args []string, j int) bool {
	return j < len(args) && !strings.HasPrefix(strings.TrimLeft(args[j], `"'`), "-")
}

// redactURL hides the password of URL userinfo (scheme://user:pass@host).
// The password may hold '@' and '/', so it runs up to the last '@'.
func redactURL(tok string) string {
	loc := urlScheme.FindStringIndex(tok)
	if loc == nil {
		return tok
	}
	rest := tok[loc[1]:]
	at := strings.LastIndexByte(rest, '@')
	if at < 0 {
		return tok
	}
	user, _, hasPass := strings.Cut(rest[:at], ":")
	if !hasPass {
		return tok
	}
	return tok[:loc[1]] + user + ":***@" + rest[at+1:]
}

// redactCmdline renders a raw /proc cmdline for display. Compose commands
// and entrypoints carry secrets as arguments (--requirepass s3cret,
// --password=x, DB_PASSWORD=x, postgres://user:pw@host, curl -H
// "Authorization: Bearer x"), and secret values never leave the agent, so
// those values become "***" here, before the cap. It is best-effort: it
// knows common shapes (shell quotes, client password options such as
// mysql -p and redis-cli -a, curl -u user:pass, URL userinfo, auth
// headers), not every way a program takes a secret.
func redactCmdline(b []byte) string {
	// Tokens, not arguments: sh -c "redis-server --requirepass x" holds a
	// whole command line in one argument. The output is space-joined anyway.
	var args []string
	for _, a := range strings.Split(string(b), "\x00") {
		args = append(args, strings.Fields(a)...)
	}
	out := make([]string, 0, len(args))
	var pass []passOpt // password options of the clients seen so far
	user := false      // a curl/wget was seen
	for i := 0; i < len(args); i++ {
		tok := args[i]
		bare := strings.Trim(tok, `"'`)
		if o, ok := passOpts[path.Base(bare)]; ok {
			pass = append(pass, o)
		}
		if userClients[path.Base(bare)] {
			user = true
		}
		if m := assignArg.FindStringSubmatch(tok); m != nil && secretName(m[1]) {
			s, j := redactTail(args, i, len(m[0]))
			out = append(out, s)
			i = j
			continue
		}
		if s, j, ok := redactPassOpt(args, i, &pass); ok {
			out = append(out, s...)
			i = j
			continue
		}
		if user {
			if s, j, ok := redactUserOpt(args, i); ok {
				out = append(out, s...)
				i = j
				continue
			}
		}
		if s, j, ok := redactHeader(args, i); ok {
			out = append(out, s...)
			i = j
			continue
		}
		if flagArg.MatchString(tok) && secretName(tok) && canRedactNext(args, i+1) {
			out = append(out, tok)
			s, k := redactNext(args, i+1, openWrap(tok))
			out = append(out, s)
			i = k
			continue
		}
		out = append(out, redactURL(tok))
	}
	s := strings.Join(out, " ")
	if len(s) > cmdlineCap {
		s = s[:cmdlineCap]
	}
	return strings.TrimRight(s, " ")
}

// redactPassOpt redacts a password given to a client's option (-psecret,
// -a secret). It returns the output tokens and the last token used; ok is
// false when args[i] is no such option.
func redactPassOpt(args []string, i int, pass *[]passOpt) ([]string, int, bool) {
	tok := args[i]
	lead := len(tok) - len(strings.TrimLeft(tok, `"'`))
	for k, o := range *pass {
		var out []string
		j := i
		switch rest := tok[lead:]; {
		case o.attached && strings.HasPrefix(rest, o.flag) && strings.Trim(rest[len(o.flag):], `"'`) != "" &&
			!strings.HasPrefix(rest[len(o.flag):], "-"):
			var s string
			s, j = redactTail(args, i, lead+len(o.flag))
			out = []string{s}
		case o.next && strings.Trim(tok, `"'`) == o.flag && canRedactNext(args, i+1):
			var s string
			s, j = redactNext(args, i+1, openWrap(tok))
			out = []string{tok, s}
		default:
			continue
		}
		if o.once {
			*pass = append((*pass)[:k:k], (*pass)[k+1:]...)
		}
		return out, j, true
	}
	return nil, i, false
}

// redactUserOpt keeps the user of curl's -u user:password (--user,
// -uuser:password, --user=user:password, and the proxy forms) and redacts
// the password. ok is false when args[i] is no such option or its value has
// no ':' (curl then prompts).
func redactUserOpt(args []string, i int) ([]string, int, bool) {
	tok := args[i]
	bare := strings.TrimLeft(tok, `"'`)
	lead := len(tok) - len(bare)
	if userFlags[strings.TrimRight(bare, `"'`)] {
		if !canRedactNext(args, i+1) {
			return nil, i, false
		}
		s, j, ok := redactCredential(args, i+1, 0)
		return []string{tok, s}, j, ok
	}
	for flag := range userFlags {
		start := len(flag) // -uadmin:pw
		if strings.HasPrefix(flag, "--") {
			start++ // --user=admin:pw
			flag += "="
		}
		if strings.HasPrefix(bare, flag) && len(bare) > len(flag) {
			s, j, ok := redactCredential(args, i, lead+start)
			return []string{s}, j, ok
		}
	}
	return nil, i, false
}

// redactCredential redacts what follows the first ':' of a user:password
// value starting at args[i][start:].
func redactCredential(args []string, i, start int) (string, int, bool) {
	colon := strings.IndexByte(args[i][start:], ':')
	if colon < 0 {
		return "", i, false
	}
	s, j := redactTail(args, i, start+colon+1)
	return s, j, true
}

// redactHeader redacts an HTTP header with a secret name: its value may
// follow in the next tokens ("Authorization: Bearer x", also after wget's
// "--header=") or, in a header given to -H / --header / --header=, be
// attached ("X-Api-Key:abc", "Authorization:Bearer x"). An auth scheme
// (Bearer, Basic, ...) is kept. ok is false when args[i] is no such header.
func redactHeader(args []string, i int) ([]string, int, bool) {
	tok := args[i]
	m := headerArg.FindStringSubmatch(tok)
	if m == nil || !secretName(m[1]) {
		return nil, i, false
	}
	rest := strings.Trim(tok[len(m[0]):], `"'`)
	if rest != "" {
		// Attached values only in a header argument: elsewhere a
		// "keycloak:8080" is a host and port.
		inHeader := strings.Contains(m[0], "--header=") || i > 0 && headerFlags[strings.Trim(args[i-1], `"'`)]
		if !inHeader {
			return nil, i, false
		}
		if !authSchemes[strings.ToLower(rest)] {
			s, j := redactTail(args, i, len(m[0]))
			return []string{s}, j, true
		}
	}
	out := []string{tok}
	wrap, j := openQuote(tok), i+1
	if rest != "" && wrap == 0 && strings.ContainsAny(tok, `"'`) {
		return out, i, true // "Authorization:Bearer" closes its quote: no value
	}
	if !canRedactNext(args, j) {
		return out, i, true
	}
	if rest == "" && authSchemes[strings.ToLower(strings.Trim(args[j], `"'`))] {
		out = append(out, args[j])
		if wrap != 0 && strings.IndexByte(args[j], wrap) >= 0 {
			return out, j, true
		}
		j++
		if !canRedactNext(args, j) {
			return out, j - 1, true
		}
	}
	s, k := redactNext(args, j, wrap)
	return append(out, s), k, true
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

var (
	dockerScope = regexp.MustCompile(`^docker-([0-9a-f]{64})\.scope$`)
	dockerID    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// groupFromCgroup returns a process's group: its cgroup v2 path (the "0::"
// line of /proc/<pid>/cgroup), or "" when it has none: the root cgroup
// (kernel threads), a cgroup v1 host, or a path that is not absolute or
// climbs with "..". A container process is grouped at its Docker scope
// (docker-<id>.scope or docker/<id>), so sub-cgroups inside the container
// add up under it; containerID is then the container's id.
func groupFromCgroup(b []byte) (id, containerID string) {
	var raw string
	found := false
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "0::"); ok {
			raw, found = strings.TrimSuffix(v, " (deleted)"), true
			break
		}
	}
	if !found || !strings.HasPrefix(raw, "/") {
		return "", ""
	}
	parts := strings.Split(raw, "/")
	for _, el := range parts {
		if el == ".." {
			return "", ""
		}
	}
	for i, el := range parts {
		if m := dockerScope.FindStringSubmatch(el); m != nil {
			return path.Clean(strings.Join(parts[:i+1], "/")), m[1]
		}
		if el == "docker" && i+1 < len(parts) && dockerID.MatchString(parts[i+1]) {
			return path.Clean(strings.Join(parts[:i+2], "/")), parts[i+1]
		}
	}
	if id = path.Clean(raw); id == "/" {
		return "", ""
	}
	return id, ""
}

// groupKind names a group's kind and label (its leaf, empty for a
// container, which the UI names by its app).
func groupKind(id, containerID string) (kind, label string) {
	if containerID != "" {
		return "container", ""
	}
	label = path.Base(id)
	switch {
	case strings.HasSuffix(label, ".service"):
		return "service", label
	case strings.HasSuffix(label, ".scope"):
		return "scope", label
	case strings.HasSuffix(label, ".slice"):
		return "slice", label
	}
	return "other", label
}

// parseIOStat sums rbytes and wbytes over a cgroup v2 io.stat's lines
// ("MAJ:MIN rbytes=N wbytes=N rios=N ..."), one per device. An empty file
// (no I/O yet) is 0/0.
func parseIOStat(b []byte) (read, write uint64) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		for _, kv := range f[min(1, len(f)):] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				continue
			}
			switch k {
			case "rbytes":
				read += n
			case "wbytes":
				write += n
			}
		}
	}
	return read, write
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
