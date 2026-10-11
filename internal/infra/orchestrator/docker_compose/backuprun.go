package dockercompose

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/helper"
	"winterflow/internal/infra/backup/layout"
	"winterflow/internal/infra/backup/store"
	"winterflow/pkg/version"
)

// Time limits of a backup run (the scheduler bounds the whole run: 30
// minutes for dump plus upload).
const (
	helperPullTimeout  = 5 * time.Minute  // the image pull on first use
	storageCallTimeout = 30 * time.Second // one list, manifest or delete call
	dockerCallTimeout  = 30 * time.Second // one short docker call (inspect, rm)
	helperStopGrace    = 10 * time.Second // after a kill, for docker's output to drain
)

// putAbortGrace is how long an aborted upload gets to clean up after itself
// (an S3 multipart abort) on a live context before that context is cancelled.
var putAbortGrace = 30 * time.Second

// Helper container limits: a run must not starve the apps it backs up on a
// small box, and gets no network and only the capabilities a dump needs
// (read every file; switch to a database's owner). The log driver is off: the
// dump travels over stdout, and docker's logging driver would otherwise write
// every byte of it a second time (into json-file, or into journald/syslog,
// where a database's contents would outlive the run).
var helperLimits = []string{
	"--log-driver", "none",
	"--network", "none", "--memory", "256m", "--cpu-shares", "256",
	"--cap-drop", "ALL", "--cap-add", "DAC_OVERRIDE", "--cap-add", "SETUID", "--cap-add", "SETGID",
	"--security-opt", "no-new-privileges",
}

// BackupJob is one run of a backup.
type BackupJob struct {
	AppID  string
	Backup string
	Kind   command.PointKind
}

// BackupResult is the outcome of a run: a new point, or Unchanged (nothing
// uploaded). Fingerprint is the source's, Size the stored size.
type BackupResult struct {
	Unchanged   bool
	Point       *command.RestorePoint
	Fingerprint string
	Size        int64
	Warnings    []string
}

// errNotBackedUp aborts an upload the helper produced nothing for.
var errNotBackedUp = errors.New("no backup was produced")

// ErrNotRunnable marks a run refused before it started: the backup is off,
// custom, or not the app's. The scheduler records no attempt for it.
var ErrNotRunnable = errors.New("backup not runnable")

// ErrNothingToBackUp marks a run that found nothing to copy: the database
// file is missing, or none of the paths exist (helper exit 4). A restore
// skips its pre-restore copy on it.
var ErrNothingToBackUp = errors.New("nothing to back up")

// nothingToBackUp is an ErrNothingToBackUp with the helper's reason.
type nothingToBackUp string

func (e nothingToBackUp) Error() string        { return string(e) }
func (e nothingToBackUp) Is(target error) bool { return target == ErrNothingToBackUp }

// notRunnable is an ErrNotRunnable with its reason as the message.
type notRunnable string

func (e notRunnable) Error() string        { return string(e) }
func (e notRunnable) Is(target error) bool { return target == ErrNotRunnable }

// RunBackup runs one sqlite or files backup: the helper container dumps the
// data from the app's own mounts, the agent compresses it (zstd) and streams
// it to the backup's storage with its sha256, writes the manifest last,
// then prunes by the backup's retention under BACKUP_RETENTION_DAYS. When the
// data hasn't changed since the newest copy, nothing is uploaded — unless
// that copy is about to reach the ceiling (or the run is a pre-restore
// copy). ctx bounds the whole run.
func (r *Repository) RunBackup(ctx context.Context, job BackupJob) (BackupResult, error) {
	dir := r.appDataDir(job.AppID)
	if !r.appExists(job.AppID) {
		return BackupResult{}, notRunnable("app " + job.AppID + " not found")
	}
	files := r.appComposeFiles(dir)
	cfg, err := r.appBackups(dir, files)
	if err != nil {
		return BackupResult{}, err
	}
	b, err := runnableBackup(cfg.backups, job.Backup)
	if err != nil {
		return BackupResult{}, err
	}
	st, err := r.openBackupStorage(b.Storage)
	if err != nil {
		return BackupResult{}, err
	}
	plans, err := r.resolveBackups(ctx, job.AppID, files, []recipe.EffectiveBackup{b})
	if err != nil {
		return BackupResult{}, err
	}
	plan := plans[b.Name]
	folder := layout.Folder(job.AppID, b.Name)
	points, err := listFolder(ctx, st, folder)
	if err != nil {
		return BackupResult{}, err
	}
	ceiling := r.cfg.GetBackupRetentionDays()
	now := r.now().UTC()
	res := BackupResult{Warnings: plan.Warnings}
	since := ""
	if job.Kind != command.PointPreRestore && !layout.NeedsRefresh(points, ceiling, now) {
		if p, ok := layout.Newest(points, command.PointScheduled, command.PointManual); ok {
			m, err := readManifest(ctx, st, p.ManifestKey)
			if err != nil {
				res.Warnings = append(res.Warnings, "the newest copy's manifest could not be read, so a full copy was made: "+err.Error())
			}
			since = m.Fingerprint
		}
	}
	exe, err := r.helperBinary()
	if err != nil {
		return BackupResult{}, fmt.Errorf("find the agent executable: %w", err)
	}
	if err := r.ensureHelperImage(ctx); err != nil {
		return BackupResult{}, err
	}
	if err := r.checkHelperVolumes(ctx, plan); err != nil {
		return BackupResult{}, err
	}
	id := layout.PointID(now, job.Kind)
	name := "wf-backup-" + strconv.FormatInt(now.UnixNano(), 36)
	args := helperArgs(name, job.AppID, exe, plan, b, since)
	dataKey := layout.DataKey(job.AppID, b.Name, id, b.Type)
	dump, err := r.dumpAndUpload(ctx, st, dataKey, name, args)
	res.Warnings = append(res.Warnings, dump.result.Warnings...)
	switch {
	case err != nil:
		return res, err
	case dump.unchanged:
		res.Unchanged = true
		res.Fingerprint = since
	default:
		m := layout.Manifest{
			AppID: job.AppID, Backup: b.Name, Type: b.Type, Kind: job.Kind, Created: now,
			SHA256: dump.sha256, Size: dump.size, RawSize: dump.result.RawSize,
			Fingerprint: dump.result.Fingerprint, AgentVersion: version.GetVersion(),
		}
		if err := writeManifest(ctx, st, layout.ManifestKey(job.AppID, b.Name, id), m); err != nil {
			return res, err
		}
		p := layout.Point{ID: id, Time: now.Truncate(time.Second), Kind: job.Kind, DataKey: dataKey,
			ManifestKey: layout.ManifestKey(job.AppID, b.Name, id), Size: dump.size}
		info := p.Info(ceiling)
		res.Point, res.Fingerprint, res.Size = &info, m.Fingerprint, dump.size
		points = append([]layout.Point{p}, points...)
	}
	policy := layout.Policy{Keep: b.Keep.Retention(), CeilingDays: ceiling}
	if err := deletePoints(ctx, st, layout.Prune(points, policy, now)); err != nil {
		res.Warnings = append(res.Warnings, "retention: "+err.Error())
	}
	return res, nil
}

// runnableBackup finds a backup the agent runs: one of the app's, enabled,
// sqlite or files.
func runnableBackup(backups []recipe.EffectiveBackup, name string) (recipe.EffectiveBackup, error) {
	for _, b := range backups {
		if b.Name != name {
			continue
		}
		switch {
		case !b.Enabled:
			return b, notRunnable("the backup " + name + " is off")
		case !b.Builtin():
			return b, notRunnable("the backup " + name + " is a custom backup: its own service runs it")
		}
		return b, nil
	}
	return recipe.EffectiveBackup{}, notRunnable("the app has no backup " + name)
}

// openBackupStorage opens a storage of this server by name, its secret
// decrypted in memory only.
func (r *Repository) openBackupStorage(name string) (store.Store, error) {
	s, ok, err := r.storages.Get(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("there is no storage %s on this server", name)
	}
	secret := ""
	if s.S3 != nil {
		if secret, err = r.storageSecret(s); err != nil {
			return nil, err
		}
	}
	return r.openStorage(s, secret)
}

// listFolder lists a backup folder's points (30s).
func listFolder(ctx context.Context, st store.Store, folder string) ([]layout.Point, error) {
	ctx, cancel := context.WithTimeout(ctx, storageCallTimeout)
	defer cancel()
	objs, err := st.List(ctx, folder)
	if err != nil {
		return nil, fmt.Errorf("list the backup's copies: %w", err)
	}
	return layout.Points(objs, folder), nil
}

func readManifest(ctx context.Context, st store.Store, key string) (layout.Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, storageCallTimeout)
	defer cancel()
	rc, err := st.Get(ctx, key)
	if err != nil {
		return layout.Manifest{}, err
	}
	defer rc.Close()
	var m layout.Manifest
	if err := json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&m); err != nil {
		return layout.Manifest{}, err
	}
	return m, nil
}

func writeManifest(ctx context.Context, st store.Store, key string, m layout.Manifest) error {
	ctx, cancel := context.WithTimeout(ctx, storageCallTimeout)
	defer cancel()
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := st.Put(ctx, key, strings.NewReader(string(raw)+"\n")); err != nil {
		return fmt.Errorf("write the manifest: %w", err)
	}
	return nil
}

// deletePoints removes the points' objects (30s).
func deletePoints(ctx context.Context, st store.Store, points []layout.Point) error {
	var keys []string
	for _, p := range points {
		keys = append(keys, p.Keys()...)
	}
	if len(keys) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, storageCallTimeout)
	defer cancel()
	return st.Delete(ctx, keys...)
}

// helperArgs is the `docker run` of one dump: the backup's sources mounted
// at /src/<n> (read-only for files; sqlite needs to take its locks), the
// agent's own static binary at /wf.
func helperArgs(name, appID, exe string, plan helperPlan, b recipe.EffectiveBackup, since string) []string {
	args := []string{"run", "--rm", "--name", name}
	args = append(args, helperLimits...)
	args = append(args, "--label", helper.RoleLabel, "--label", helper.AppLabel+"="+appID)
	ro := b.Type == recipe.TypeFiles
	for i, m := range plan.Mounts {
		spec := "type=volume," + csvField("source="+m.Volume)
		if m.Bind != "" {
			spec = "type=bind," + csvField("source="+m.Bind)
		}
		spec += ",target=/src/" + strconv.Itoa(i)
		if ro {
			spec += ",readonly"
		}
		args = append(args, "--mount", spec)
	}
	args = append(args, "--mount", "type=bind,"+csvField("source="+exe)+",target=/wf,readonly", helper.Image, "/wf", "backup", "dump")
	if b.Type == recipe.TypeSQLite {
		args = append(args, "sqlite", plan.Paths[0].Src(), "--entry", plan.Paths[0].Entry)
	} else {
		args = append(args, "files")
		for _, p := range plan.Paths {
			args = append(args, "--src", p.Src(), "--entry", p.Entry)
		}
		for _, e := range b.Exclude {
			args = append(args, "--exclude", e)
		}
	}
	if since != "" {
		args = append(args, "--since", since)
	}
	return args
}

// csvField quotes one field of a --mount value (docker parses it as CSV):
// a path with a comma or a quote must not split the spec.
func csvField(f string) string {
	if !strings.ContainsAny(f, ",\"") {
		return f
	}
	return `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
}

// dumpOutcome is what the helper produced and what was uploaded.
type dumpOutcome struct {
	unchanged bool
	result    helper.Result
	sha256    string
	size      int64
}

// lazyUpload is the destination of the compressed dump: the upload to the
// storage starts with the first byte the helper writes, so a run that
// produces nothing (unchanged, or failing before any output) never touches
// the storage. The upload runs on a context detached from the run's deadline,
// so that when a run is cut short the storage driver can abort cleanly (an S3
// multipart abort needs a live context); it is cancelled only after a grace.
// When the run's own context ends, the pipe is closed at once (a helper's
// output copier blocked in Write on a stalled storage must be released), and
// the upload context is cancelled after the same grace.
type lazyUpload struct {
	st  store.Store
	key string

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	started bool
	aborted bool
	selfErr error // the upload failed on its own, before any abort
	pr      *io.PipeReader
	pw      *io.PipeWriter
	done    chan error
	stop    func() bool // detaches the run-context hook
}

func newLazyUpload(runCtx context.Context, st store.Store, key string) *lazyUpload {
	pctx, cancel := context.WithCancel(context.WithoutCancel(runCtx))
	u := &lazyUpload{st: st, key: key, ctx: pctx, cancel: cancel, done: make(chan error, 1)}
	u.stop = context.AfterFunc(runCtx, func() {
		u.mu.Lock()
		u.aborted = true
		pw := u.pw
		u.mu.Unlock()
		if pw != nil {
			pw.CloseWithError(runCtx.Err()) // releases a Write blocked on a stalled upload
		}
		time.AfterFunc(putAbortGrace, cancel)
	})
	return u
}

func (u *lazyUpload) Write(p []byte) (int, error) {
	u.mu.Lock()
	if u.aborted {
		u.mu.Unlock()
		return 0, errNotBackedUp
	}
	if !u.started {
		u.started = true
		u.pr, u.pw = io.Pipe()
		go u.put(u.pr)
	}
	pw := u.pw
	u.mu.Unlock()
	return pw.Write(p)
}

func (u *lazyUpload) put(pr *io.PipeReader) {
	err := u.st.Put(u.ctx, u.key, pr)
	u.mu.Lock()
	if err != nil && !u.aborted {
		u.selfErr = err
	}
	u.mu.Unlock()
	pr.CloseWithError(errNotBackedUp) // a failed upload fails the writes, never blocks them
	u.done <- err
}

// finish ends a successful dump: the upload's data is complete. It waits for
// the upload (until ctx ends, then the grace) and reports its outcome.
func (u *lazyUpload) finish(ctx context.Context) error {
	u.mu.Lock()
	started, pw := u.started, u.pw
	u.mu.Unlock()
	if !started {
		return errNotBackedUp
	}
	pw.Close()
	select {
	case err := <-u.done:
		return err
	case <-ctx.Done():
	}
	// The run's time is up: let the upload abort cleanly.
	u.abort()
	return fmt.Errorf("stopped after its time limit: %w", ctx.Err())
}

// abort ends an upload that must leave no object. The pipe is closed with an
// error first, so the driver's read fails and it cleans up on a live context;
// the context is cancelled only after the grace. It returns the error of an
// upload that had failed by itself, if any.
func (u *lazyUpload) abort() error {
	u.mu.Lock()
	u.aborted = true
	started, pw := u.started, u.pw
	u.mu.Unlock()
	if started {
		pw.CloseWithError(errNotBackedUp)
		select {
		case <-u.done:
		case <-time.After(putAbortGrace):
		}
	}
	u.cancel()
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.selfErr
}

// dumpAndUpload runs the helper and streams its stdout through zstd into
// key, hashing what is stored. Unless the helper succeeds with a result, the
// upload is aborted, which leaves no object behind.
func (r *Repository) dumpAndUpload(ctx context.Context, st store.Store, key, name string, args []string) (dumpOutcome, error) {
	up := newLazyUpload(ctx, st, key)
	defer up.cancel()
	defer up.stop()
	sum := sha256.New()
	stored := &countWriter{}
	enc, _ := zstd.NewWriter(io.MultiWriter(up, sum, stored), zstd.WithEncoderConcurrency(1))
	exit, stderr, runErr := r.runHelper(ctx, name, args, enc)
	res, parsed := helper.ParseResult(stderr)
	out := dumpOutcome{result: res}
	var encErr error
	closed := false
	if runErr == nil && exit == helper.ExitOK && parsed {
		encErr, closed = enc.Close(), true
		if encErr == nil {
			if err := up.finish(ctx); err != nil {
				return out, fmt.Errorf("upload the copy: %w", err)
			}
			out.sha256, out.size = hex.EncodeToString(sum.Sum(nil)), stored.n
			return out, nil
		}
	}
	// An upload that failed by itself is the cause (the helper then died
	// writing to it); otherwise abort it.
	early := up.abort()
	if !closed {
		_ = enc.Close() // its writes fail now: this only releases the encoder
	}
	switch {
	case early != nil:
		return out, fmt.Errorf("upload the copy: %w", early)
	case runErr != nil:
		return out, fmt.Errorf("the backup helper: %w", runErr)
	case exit == helper.ExitUnchanged:
		out.unchanged = true
		return out, nil
	case exit == helper.ExitMissing && parsed && res.Error != "":
		return out, nothingToBackUp(res.Error)
	case exit == helper.ExitOK && encErr != nil:
		return out, fmt.Errorf("compress the copy: %w", encErr)
	case exit == helper.ExitOK && !parsed:
		return out, errors.New("the backup helper reported no result")
	case parsed && res.Error != "":
		return out, errors.New(res.Error)
	default:
		return out, fmt.Errorf("the backup helper exited with %d: %s", exit, lastLines(stderr, 3))
	}
}

// lastLines is the end of a command's output, for an error.
func lastLines(out []byte, n int) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " / ")
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// tailBuffer keeps the last max bytes written (a helper's stderr).
type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

// runDockerHelper runs `docker run` (args) with stdout to out. When ctx ends
// the helper container is removed (docker rm -f) and the CLI killed;
// docker's output gets helperStopGrace to drain. Whenever the run does not
// end in the helper's own exit (the CLI died on a failing out, was killed, or
// docker itself failed), the container is removed too: --rm only fires once
// the container exits, and one nobody reads from would block on its next
// write forever.
func runDockerHelper(ctx context.Context, name string, args []string, out io.Writer) (int, []byte, error) {
	return runDockerIO(ctx, name, args, nil, out)
}

// runDockerRestoreHelper is runDockerHelper for a restore: in is the
// helper's stdin (its args carry -i), its stdout is discarded.
func runDockerRestoreHelper(ctx context.Context, name string, args []string, in io.Reader) (int, []byte, error) {
	return runDockerIO(ctx, name, args, in, io.Discard)
}

// runDockerIO runs a helper's `docker run` with stdin in (nil: none) and
// stdout out.
func runDockerIO(ctx context.Context, name string, args []string, in io.Reader, out io.Writer) (int, []byte, error) {
	stderr := &tailBuffer{max: 64 << 10}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, stderr
	cmd.Cancel = func() error {
		removeHelper(name)
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = helperStopGrace
	err := cmd.Run()
	if ctx.Err() != nil {
		// The container may still have been being created when the CLI was
		// killed: remove it again once the daemon has had time to finish.
		removeHelper(name)
		time.Sleep(time.Second)
		removeHelper(name)
		return -1, stderr.buf, fmt.Errorf("stopped after its time limit: %w", ctx.Err())
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code := ee.ExitCode()
		if code != helper.ExitOK && code != helper.ExitUnchanged && code != helper.ExitMissing && code != helper.ExitPartial {
			removeHelper(name)
		}
		return code, stderr.buf, nil
	}
	if err != nil {
		removeHelper(name)
		return -1, stderr.buf, err
	}
	return 0, stderr.buf, nil
}

// removeHelper force-removes a helper container (best effort, bounded).
func removeHelper(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), dockerCallTimeout)
	defer cancel()
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
}

// ensureHelperImage pulls the helper image once (5 minutes); later runs
// only remember that it is there.
func (r *Repository) ensureHelperImage(ctx context.Context) error {
	if r.helperImageReady.Load() {
		return nil
	}
	ictx, cancel := context.WithTimeout(ctx, dockerCallTimeout)
	err := r.docker(ictx, "image", "inspect", "--format", "{{.Id}}", helper.Image)
	cancel()
	if err != nil {
		pctx, cancel := context.WithTimeout(ctx, helperPullTimeout)
		defer cancel()
		if err := r.docker(pctx, "pull", helper.Image); err != nil {
			return fmt.Errorf("pull the backup helper image %s: %w", helper.Image, err)
		}
	}
	r.helperImageReady.Store(true)
	return nil
}

// checkHelperVolumes requires every Docker volume of a plan to exist:
// mounting a missing one would create it, empty, outside compose.
func (r *Repository) checkHelperVolumes(ctx context.Context, plan helperPlan) error {
	for _, m := range plan.Mounts {
		if m.Volume == "" {
			continue
		}
		vctx, cancel := context.WithTimeout(ctx, dockerCallTimeout)
		err := r.docker(vctx, "volume", "inspect", "--format", "{{.Name}}", m.Volume)
		cancel()
		if err != nil {
			return fmt.Errorf("the volume %s does not exist (has the app been started?): %w", m.Volume, err)
		}
	}
	return nil
}

// RemoveHelpers removes helper containers left behind by an agent that
// stopped mid-run (one `docker ps` and at most one `docker rm`) and the
// restore downloads it left in backups/tmp.
func (r *Repository) RemoveHelpers(ctx context.Context) error {
	r.removeRestoreDownloads()
	return r.removeHelpers(ctx)
}

// removeHelpers is RemoveHelpers narrowed by extra `docker ps` filters (a
// test removes only its own helpers, never a real backup in progress on the
// same host).
func (r *Repository) removeHelpers(ctx context.Context, extra ...string) error {
	ctx, cancel := context.WithTimeout(ctx, dockerCallTimeout)
	defer cancel()
	args := []string{"ps", "-aq", "--filter", "label=" + helper.RoleLabel}
	for _, f := range extra {
		args = append(args, "--filter", f)
	}
	out, err := execOutput(ctx, "docker", args...)
	if err != nil {
		return err
	}
	if ids := strings.Fields(string(out)); len(ids) > 0 {
		return dockerCombined(ctx, append([]string{"rm", "-f"}, ids...)...)
	}
	return nil
}

// errNotStatic is why backups can't run from a dynamically linked binary.
var errNotStatic = errors.New("this winterflow binary is not static (build with CGO_ENABLED=0): backups need it")

// staticVerdict caches the one-time check of the running executable.
var staticVerdict struct {
	once sync.Once
	exe  string
	err  error
}

// agentExecutable is the binary the helper runs: this process's own,
// symlinks resolved. The helper runs it inside alpine (no glibc loader), so
// it must be statically linked; the check runs once per process.
func agentExecutable() (string, error) {
	staticVerdict.once.Do(func() {
		exe, err := os.Executable()
		if err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err == nil {
			err = checkStatic(exe)
		}
		staticVerdict.exe, staticVerdict.err = exe, err
	})
	return staticVerdict.exe, staticVerdict.err
}

// checkStatic refuses an executable that is not a static ELF: one with a
// program interpreter (PT_INTERP) can't start in the helper image.
func checkStatic(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		var fe *elf.FormatError
		if errors.As(err, &fe) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("%s is not a Linux executable: backups need a Linux build", path)
		}
		return err
	}
	defer f.Close()
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			return errNotStatic
		}
	}
	return nil
}
