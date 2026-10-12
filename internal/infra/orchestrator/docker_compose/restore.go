package dockercompose

import (
	"archive/tar"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/klauspost/compress/zstd"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/helper"
	"winterflow/internal/infra/backup/layout"
	"winterflow/internal/infra/backup/store"
)

// ErrAppDeleted is the cause a restore's context is cancelled with when its
// app is being deleted: the app is then not started again.
var ErrAppDeleted = errors.New("the app is being deleted")

// restartTimeout bounds starting an app again after a restore, on a context
// of its own (the restore's may be over).
const restartTimeout = 2 * time.Minute

// RestoreStopBudget is the longest a restore takes to wind down after its
// context ends, in the engine's own bounded steps: the kill of the helper
// (docker rm -f, the wait for its output, two more removals and the second
// the daemon is given) and then compose down or compose start. The scheduler
// gives a restore at least this long past its deadline before it gives up on
// it, so the engine's real outcome (the app left stopped, the pre-restore
// point to go back to) is the one that gets recorded.
const RestoreStopBudget = 3*dockerCallTimeout + helperStopGrace + time.Second + restartTimeout

// restoreMinBudget is the least of a restore's time budget that must remain
// before the app is stopped: the pre-restore copy and the helper still have
// to run, and a helper cut short by the budget leaves the data in doubt.
const restoreMinBudget = 10 * time.Minute

// restoreExtractRate is the rate, in raw bytes per second, a restore is
// assumed to put data back at on the slowest box (a Raspberry Pi's SD card):
// the time left is checked against the size of the copy.
const restoreExtractRate = 10 << 20

// restoreNeeds is the time a restore needs to put m's data back, and so the
// least of the budget that must remain before the app is stopped (and again
// before the helper starts): the floor, or what the copy's raw size takes at
// restoreExtractRate.
func restoreNeeds(m layout.Manifest) time.Duration {
	return max(restoreMinBudget, time.Duration(max(m.RawSize, 0)/restoreExtractRate)*time.Second)
}

// restoreDownloadMargin is the room beyond a download's size the disk must
// have free: the agent's own database and logs live on it too.
const restoreDownloadMargin = 64 << 20

// restoreWaitForDelete is how long DeleteApp waits for a restore of its app
// to end.
const restoreWaitForDelete = 2 * time.Minute

// ErrRestoring is why an operation on an app is refused while a restore is
// putting its data back.
var ErrRestoring = errors.New("a restore of this app is in progress")

// ErrAppBusy is why a restore does not begin: operations on the app are
// still running.
var ErrAppBusy = errors.New("the app is busy")

// ErrAgentUpdating is why a restore does not begin: the agent has replaced
// its binary and is about to exit.
var ErrAgentUpdating = errors.New("the agent is updating and about to restart")

// ErrRestoreRunning is why the agent does not update itself: its exit would
// cut a restore short, with the app stopped and its data perhaps half
// replaced.
var ErrRestoreRunning = errors.New("the agent is not updated while a backup is being restored: try again once the restore has ended")

// beginUpdate refuses an agent update while any restore runs, and from then
// on refuses a new restore (the agent is about to exit). failed undoes it
// when the update did not go through.
func (r *Repository) beginUpdate() (failed func(), err error) {
	r.restoreMu.Lock()
	defer r.restoreMu.Unlock()
	if len(r.restores) > 0 {
		return nil, ErrRestoreRunning
	}
	r.updating = true
	return func() {
		r.restoreMu.Lock()
		r.updating = false
		r.restoreMu.Unlock()
	}, nil
}

// restoreLimits are a restore helper's: a dump's, plus what putting files
// back needs (give them their owner; set the mode and times of files it
// doesn't own; keep a folder's setgid bit when its group is set).
var restoreLimits = append(slices.Clone(helperLimits), "--cap-add", "CHOWN", "--cap-add", "FOWNER", "--cap-add", "FSETID")

// statfsFree is the room left on the file system holding path.
func statfsFree(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// beginRestore marks the app as being restored, from just before it is
// stopped until end is called: lifecycle operations on it (start, stop,
// restart, update, save, rollback, rename, backup settings, source polls,
// storage redeploys) are refused with ErrRestoring meanwhile, so nothing
// starts containers on the data the helper is replacing. It refuses a second
// restore of the same app.
func (r *Repository) beginRestore(appID string) (end func(), err error) {
	r.restoreMu.Lock()
	defer r.restoreMu.Unlock()
	if r.updating {
		return nil, ErrAgentUpdating
	}
	if _, busy := r.restores[appID]; busy {
		return nil, ErrRestoring
	}
	if r.ops[appID] > 0 {
		return nil, fmt.Errorf("%w (an operation on it, such as a deploy, is still running); try again", ErrAppBusy)
	}
	if r.restores == nil {
		r.restores = map[string]chan struct{}{}
	}
	done := make(chan struct{})
	r.restores[appID] = done
	return func() {
		r.restoreMu.Lock()
		defer r.restoreMu.Unlock()
		if r.restores[appID] == done {
			delete(r.restores, appID)
		}
		close(done)
	}, nil
}

// beginOp registers an operation that may start containers (start, restart,
// update, save, rollback, a deploy) on the app, until end is called: it is
// refused with ErrRestoring while a restore runs, and a restore does not
// begin while one is registered (ErrAppBusy), so an operation that passed
// its check can never run beside the swap. Operations nest (Start calls
// deploy).
func (r *Repository) beginOp(appID string) (end func(), err error) {
	r.restoreMu.Lock()
	defer r.restoreMu.Unlock()
	if _, busy := r.restores[appID]; busy {
		return nil, ErrRestoring
	}
	if r.ops == nil {
		r.ops = map[string]int{}
	}
	r.ops[appID]++
	return func() {
		r.restoreMu.Lock()
		defer r.restoreMu.Unlock()
		if n := r.ops[appID]; n > 1 {
			r.ops[appID] = n - 1
		} else {
			delete(r.ops, appID)
		}
	}, nil
}

// restoreRunning reports whether a restore of the app is running.
func (r *Repository) restoreRunning(appID string) bool {
	r.restoreMu.Lock()
	defer r.restoreMu.Unlock()
	_, busy := r.restores[appID]
	return busy
}

// refuseDuringRestore is nil unless a restore of the app is running.
func (r *Repository) refuseDuringRestore(appID string) error {
	if r.restoreRunning(appID) {
		return ErrRestoring
	}
	return nil
}

// waitForRestore waits for a restore of the app to end, for at most
// restoreWait and ctx; it then fails with ErrRestoring.
func (r *Repository) waitForRestore(ctx context.Context, appID string) error {
	r.restoreMu.Lock()
	done, busy := r.restores[appID]
	r.restoreMu.Unlock()
	if !busy {
		return nil
	}
	t := time.NewTimer(r.restoreWait)
	defer t.Stop()
	select {
	case <-done:
		return nil
	case <-t.C:
	case <-ctx.Done():
	}
	return ErrRestoring
}

// RestoreJob is one restore: Point of the app's backup.
type RestoreJob struct {
	AppID  string
	Backup string
	Point  string
}

// restoreTmpDir holds restore downloads ({AGENT_DATA_DIR}/backups/tmp).
func (r *Repository) restoreTmpDir() string {
	return filepath.Join(r.cfg.GetAgentDataDir(), "backups", "tmp")
}

// removeRestoreDownloads removes the downloads a restore cut short (the
// agent stopped mid-restore) left in backups/tmp.
func (r *Repository) removeRestoreDownloads() {
	if err := os.RemoveAll(r.restoreTmpDir()); err != nil {
		r.log.Warn("remove leftover restore downloads", "error", err)
	}
}

// RestoreBackup puts a restore point back in place:
//
//  1. the point is downloaded (size and free space checked first) and its
//     sha256 checked against its manifest; a mismatch, an unreadable archive,
//     a manifest of another type or of a location the backup no longer has,
//     or too little of the time budget left for the copy's size (looked at
//     again after the pre-restore copy) aborts before anything is replaced;
//  2. the app is marked as being restored (other operations on it are
//     refused with ErrRestoring, and the restore itself is refused with
//     ErrAppBusy while one is running), and a running app is stopped
//     (compose stop); a running app with undeployed changes is refused;
//  3. the current data is saved as a pre-restore point (a normal run of
//     kind pre-restore, which never prunes the point being restored); when
//     that fails the app is started again and nothing is restored (nothing
//     to save — no database file, none of the paths — only warns);
//  4. the helper puts the copy back (sqlite: replaces the file; files:
//     extracts beside the data, checking free space, then swaps);
//  5. the app is started again.
//
// The data is left as it was, and the app started again (unless the
// restore was stopped because the app is being deleted), only when the
// helper never ran or failed cleanly (exit 1, docker's own 125-127, a
// helper that did not start), or when a files helper was stopped from
// outside before it printed its swapping line (it had only written its temp
// folders) and that is certain: its container is confirmed removed, the
// docker CLI's output was read in full, and no .old folder is on a bind
// mount (see notSwapped). Any other end — a crash, a kill, a swap that failed half-way, a
// "success" with no result, the agent stopping the helper — may have left
// the data partly replaced: the app is left stopped
// and removed (compose down, never -v, so nothing can start it on that
// data), and the error names the pre-restore point to go back to. ctx
// bounds the whole restore.
func (r *Repository) RestoreBackup(ctx context.Context, job RestoreJob) (command.RestoreBackupResponse, error) {
	resp := command.RestoreBackupResponse{AppID: job.AppID, Backup: job.Backup, RestoredPoint: job.Point}
	if !r.appExists(job.AppID) {
		return resp, notRunnable("app " + job.AppID + " not found")
	}
	dir := r.appDataDir(job.AppID)
	files := r.appComposeFiles(dir)
	cfg, err := r.appBackups(dir, files)
	if err != nil {
		return resp, err
	}
	b, err := runnableBackup(cfg.backups, job.Backup)
	if err != nil {
		return resp, err
	}
	st, err := r.openBackupStorage(b.Storage)
	if err != nil {
		return resp, err
	}
	plans, err := r.resolveBackups(ctx, job.AppID, files, []recipe.EffectiveBackup{b})
	if err != nil {
		return resp, err
	}
	plan := plans[b.Name]
	points, err := listFolder(ctx, st, layout.Folder(job.AppID, b.Name))
	if err != nil {
		return resp, err
	}
	i := slices.IndexFunc(points, func(p layout.Point) bool { return p.ID == job.Point && p.Complete() })
	if i < 0 {
		return resp, fmt.Errorf("the backup %s has no restore point %s", b.Name, job.Point)
	}
	point := points[i]
	m, err := readManifest(ctx, st, point.ManifestKey)
	if err != nil {
		return resp, fmt.Errorf("read the restore point's manifest: %w", err)
	}
	if m.Type != b.Type {
		return resp, fmt.Errorf("the restore point is a %s copy, but the backup %s is a %s backup now", m.Type, b.Name, b.Type)
	}
	if err := checkLocation(m, plan); err != nil {
		return resp, err
	}
	if err := r.ensureHelperImage(ctx); err != nil {
		return resp, err
	}
	if err := r.checkHelperVolumes(ctx, plan); err != nil {
		return resp, err
	}
	exe, err := r.helperBinary()
	if err != nil {
		return resp, fmt.Errorf("find the agent executable: %w", err)
	}
	local, err := r.downloadPoint(ctx, st, point, m)
	if err != nil {
		return resp, err
	}
	defer os.Remove(local)
	var needs map[int]uint64
	if b.Type == recipe.TypeFiles {
		if needs, err = archiveNeeds(ctx, local, plan); err != nil {
			return resp, err
		}
	}

	// Too little time left could cut the helper short, which leaves the
	// data in doubt: refuse before anything is changed.
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < restoreNeeds(m) {
		return resp, fmt.Errorf("too little time is left to restore safely (%s; at least %s are needed): nothing was changed", time.Until(dl).Round(time.Second), restoreNeeds(m))
	}
	end, err := r.beginRestore(job.AppID)
	if err != nil {
		return resp, err
	}
	defer end()
	psCtx, cancel := context.WithTimeout(ctx, dockerCallTimeout)
	up, err := r.runningProjects(psCtx)
	cancel()
	if err != nil {
		return resp, fmt.Errorf("check whether the app is running: %w", err)
	}
	running := up[projectName(job.AppID)]
	// From here on a stop of the agent leaves the app in doubt: the marker
	// tells the next start (see InterruptedRestores).
	if err := r.setRestoreMark(RestoreMark{App: job.AppID, Backup: job.Backup, Point: job.Point,
		WasRunning: running, Phase: restorePhaseStopping, Started: r.now().UTC()}); err != nil {
		r.log.Warn("record the restore in progress", "app_id", job.AppID, "error", err)
	}
	// The marker outlives a restore that the agent's stop (ctx cancelled,
	// neither its time limit nor a delete) left in doubt: the reply of a
	// stopping agent may never get out, and the next start tells the user.
	inDoubt := false
	defer func() {
		if inDoubt && context.Cause(ctx) == context.Canceled {
			return
		}
		r.clearRestoreMark(job.AppID)
	}()
	// Compose stops the services of HEAD's compose files: with undeployed
	// changes, a container of a service HEAD no longer has would keep
	// running beside the swap.
	if running && r.hasDraft(job.AppID) {
		return resp, errors.New("the app has undeployed changes: deploy them (or roll back, or stop the app first) before restoring, so that stopping the app stops all of its containers")
	}
	if running {
		if err := r.composeRun(ctx, job.AppID, "stop"); err != nil {
			return resp, r.startAgain(ctx, job.AppID, fmt.Errorf("stop the app: %w", err))
		}
	}
	// startAgain, if the app was running: on every failure below that
	// leaves its data as it was.
	fail := func(err error) error {
		if running {
			return r.startAgain(ctx, job.AppID, err)
		}
		return err
	}

	r.updateRestoreMark(job.AppID, func(m *RestoreMark) { m.Phase = restorePhaseCopy })
	// The pre-restore run's retention must not delete the point being
	// restored (a retry after a failure would find it gone).
	pre, err := r.RunBackup(ctx, BackupJob{AppID: job.AppID, Backup: b.Name, Kind: command.PointPreRestore, Protect: job.Point})
	switch {
	case errors.Is(err, ErrNothingToBackUp):
		resp.Warnings = append(resp.Warnings, "nothing was saved before the restore: "+err.Error())
	case err != nil:
		return resp, fail(fmt.Errorf("the pre-restore copy failed, so nothing was restored: %w", err))
	case pre.Point != nil:
		resp.PreRestorePoint = pre.Point.ID
	}
	for _, w := range pre.Warnings {
		resp.Warnings = append(resp.Warnings, "the pre-restore copy: "+w)
	}

	// The pre-restore copy (and the download) may have used the time the
	// helper needs: a helper cut short by the budget leaves the data in
	// doubt, so look again, now that nothing has been replaced.
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < restoreNeeds(m) {
		note := ""
		if resp.PreRestorePoint != "" {
			note = "; the pre-restore copy " + resp.PreRestorePoint + " was kept"
		}
		return resp, fail(fmt.Errorf("too little time is left after the pre-restore copy to put the data back safely (%s; at least %s are needed): nothing was changed%s", time.Until(dl).Round(time.Second), restoreNeeds(m), note))
	}
	r.updateRestoreMark(job.AppID, func(m *RestoreMark) {
		m.Phase, m.PreRestorePoint = restorePhaseHelper, resp.PreRestorePoint
	})
	name := "wf-restore-" + strconv.FormatInt(r.now().UnixNano(), 36)
	args := restoreArgs(name, job.AppID, exe, plan, b, needs)
	exit, stderr, err := r.runZstdRestore(ctx, name, args, local)
	res, parsed := helper.ParseResult(stderr)
	resp.Warnings = append(resp.Warnings, res.Warnings...)
	// Where to go back to, for the errors that leave the app stopped.
	back := "no copy was saved before the restore (there was nothing to save), so there is nothing to go back to"
	if resp.PreRestorePoint != "" {
		back = "restore the pre-restore point " + resp.PreRestorePoint + " to go back"
	}
	if b.Type != recipe.TypeSQLite {
		// A swap that failed half-way keeps the data it could not put back.
		back += ". Data the restore could not put back stays in a .wf-restore-*.old folder at the root of the mount: move or remove it before restoring again"
	}
	// Only a helper that failed cleanly (exit 1) or never ran (docker's own
	// 125-127) leaves the data as it was. Anything else — a crash, a kill,
	// a swap that failed, a "success" with no result — may have left it
	// partly replaced (a files swap; a database's WAL set aside), and the
	// app stays stopped.
	unchanged := err == nil && (exit == helper.ExitFailed || exit == 125 || exit == 126 || exit == 127)
	notStarted := errors.Is(err, errHelperNotStarted)
	stopped := func(e error) error {
		inDoubt = true
		return r.leaveStopped(ctx, job.AppID, running, e)
	}
	switch {
	case err == nil && exit == helper.ExitOK && parsed:
	case unchanged:
		return resp, fail(fmt.Errorf("the restore failed, the app's data is unchanged: %s", reason(res, stderr, exit)))
	case notStarted:
		return resp, fail(fmt.Errorf("the restore could not start, the app's data is unchanged: %w", err))
	case err != nil && ctx.Err() != nil && b.Type == recipe.TypeFiles && r.notSwapped(plan, name, stderr, err):
		// Stopped from outside before the first rename into place: the
		// helper had only written into its own temp folders.
		return resp, fail(fmt.Errorf("the restore was stopped (%w) before it replaced any data, so the app's data is unchanged", err))
	case err != nil && b.Type == recipe.TypeSQLite:
		// Stopped from outside (its time limit, the app's deletion).
		return resp, stopped(fmt.Errorf("the restore helper was stopped (%w), so the database file may be partly replaced. The app was left stopped; %s", err, back))
	case err != nil:
		return resp, stopped(fmt.Errorf("the restore helper was stopped (%w), so the paths may be partly replaced. The app was left stopped; %s", err, back))
	case exit == helper.ExitPartial:
		return resp, stopped(fmt.Errorf("the restore stopped half-way: %s. The app was left stopped; %s", reason(res, stderr, exit), back))
	default:
		return resp, stopped(fmt.Errorf("the restore helper ended unexpectedly (%s), so the data may be partly replaced. The app was left stopped; %s", reason(res, stderr, exit), back))
	}
	if running {
		if err := r.startAgain(ctx, job.AppID, nil); err != nil {
			resp.Warnings = append(resp.Warnings, "the data was restored, but the app did not start again: "+err.Error())
		}
	}
	return resp, nil
}

// oldFolderProbe bounds the look for a restore's .old folder on the host.
const oldFolderProbe = 10 * time.Second

// notSwapped is true only when a files helper stopped from outside is known
// not to have begun replacing data: the helper's container is confirmed
// removed and the CLI's output was read in full (helperGone), the output
// has no swapping line, and none of the plan's bind mounts holds the
// restore's .old folder (the swap moves the old data into it, so it is the
// disk's own witness; a Docker volume's root can't be looked at without a
// docker exec and is not checked). Anything unknown counts as swapped: the
// app is then left stopped, as for any helper that ended in doubt.
func (r *Repository) notSwapped(plan helperPlan, name string, stderr []byte, err error) bool {
	if !helperGone(err) || helper.Swapping(stderr) {
		return false
	}
	aside := ".wf-restore-" + strings.TrimPrefix(name, "wf-restore-") + ".old"
	var paths []string
	for _, m := range plan.Mounts {
		if m.Bind != "" {
			paths = append(paths, filepath.Join(m.Bind, aside))
		}
	}
	if len(paths) == 0 {
		return true
	}
	found := make(chan bool, 1)
	go func() {
		for _, p := range paths {
			if _, err := r.hostStat(p); !errors.Is(err, os.ErrNotExist) {
				found <- true // it is there, or could not be told
				return
			}
		}
		found <- false
	}()
	t := time.NewTimer(oldFolderProbe)
	defer t.Stop()
	select {
	case f := <-found:
		return !f
	case <-t.C:
		return false
	}
}

// checkLocation refuses a copy made under entries the backup's location no
// longer has. The archive's names are container paths in the service form and
// paths inside the volume in the volume form, and a whole volume accepts any
// name: a copy restored under a changed location would be extracted
// somewhere else and report success. Manifests of older agents list no
// entries and are not checked.
func checkLocation(m layout.Manifest, plan helperPlan) error {
	if len(m.Entries) == 0 {
		return nil
	}
	have := planEntries(plan)
	show := func(entries []string) string {
		out := make([]string, len(entries))
		for i, e := range entries {
			out[i] = "/" + e
			if e == "" {
				out[i] = "the whole volume"
			}
		}
		return strings.Join(out, ", ")
	}
	for _, e := range m.Entries {
		if !slices.Contains(have, e) {
			return fmt.Errorf("the backup's location changed since this copy was made (the copy holds %s; the backup now covers %s), so it can't be restored here: nothing was changed. Put the location back, or restore it by hand", show(m.Entries), show(have))
		}
	}
	return nil
}

// reason is why a helper failed: its result's error, else its exit code and
// last lines.
func reason(res helper.Result, stderr []byte, exit int) string {
	if res.Error != "" {
		return res.Error
	}
	return fmt.Sprintf("the helper exited with %d: %s", exit, lastLines(stderr, 3))
}

// leaveStopped makes "left stopped" last: the app's containers (stopped, not
// removed) would be started again by a redeploy, or by Docker's restart
// policy after a daemon or host restart, on data that may be half replaced.
// compose down (never -v) removes them, on a context of its own bounded by
// restartTimeout; an app that was not running is left alone. An app being
// deleted is no exception: if the delete then fails, the app must not be left
// with containers over data that may be half replaced. It returns cause, with
// the failure of the removal joined to it.
func (r *Repository) leaveStopped(ctx context.Context, appID string, running bool, cause error) error {
	if !running {
		return cause
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restartTimeout)
	defer cancel()
	if err := r.composeDown(dctx, appID, false); err != nil {
		return fmt.Errorf("%w (and removing the app's containers failed, so Docker may start them again on this data: %v)", cause, err)
	}
	return cause
}

// startAgain starts a stopped app again (compose start) on a context of its
// own, bounded by restartTimeout: the restore's may have ended. An app being
// deleted is left alone. It returns cause, or the start's failure joined to
// it.
func (r *Repository) startAgain(ctx context.Context, appID string, cause error) error {
	if errors.Is(context.Cause(ctx), ErrAppDeleted) {
		return cause
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restartTimeout)
	defer cancel()
	if err := r.composeRun(sctx, appID, "start"); err != nil {
		err = fmt.Errorf("start the app again: %w", err)
		if cause == nil {
			return err
		}
		return fmt.Errorf("%w (and %v)", cause, err)
	}
	return cause
}

// downloadPoint copies a point's data object into backups/tmp, checking its
// size and sha256 against the manifest. It returns the local file (the
// caller removes it).
func (r *Repository) downloadPoint(ctx context.Context, st store.Store, p layout.Point, m layout.Manifest) (string, error) {
	if err := os.MkdirAll(r.restoreTmpDir(), 0o700); err != nil {
		return "", err
	}
	free, err := r.diskFree(r.restoreTmpDir())
	if err != nil {
		return "", fmt.Errorf("check the free space for the download: %w", err)
	}
	if need := uint64(max(m.Size, 0)) + restoreDownloadMargin; free < need {
		return "", fmt.Errorf("not enough free space to download the restore point: it takes %d bytes and %d bytes are free in %s (%d are kept spare)", m.Size, free, r.restoreTmpDir(), uint64(restoreDownloadMargin))
	}
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	local := filepath.Join(r.restoreTmpDir(), p.ID+"-"+hex.EncodeToString(rnd[:])+".zst")
	f, err := os.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(local)
		}
	}()
	rc, err := st.Get(ctx, p.DataKey)
	if err != nil {
		f.Close()
		return "", fmt.Errorf("download the restore point: %w", err)
	}
	sum := sha256.New()
	// One byte past the manifest's size is enough to see the object is
	// bigger; a local folder's file ignores ctx, hence the reader.
	n, err := io.Copy(io.MultiWriter(f, sum), io.LimitReader(ctxReader{ctx, rc}, m.Size+1))
	rc.Close()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("download the restore point: %w", err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); n != m.Size || got != m.SHA256 {
		return "", fmt.Errorf("the restore point %s is damaged: its data does not match its manifest (%d bytes, sha256 %s; the manifest says %d bytes, %s)", p.ID, n, got, m.Size, m.SHA256)
	}
	ok = true
	return local, nil
}

// archiveNeeds reads a downloaded files archive's headers and sums, per
// helper mount, the room its entries take once extracted (each file rounded
// up to 4 KiB blocks, each folder one block). The helper refuses to start a
// restore a mount has no room for.
func archiveNeeds(ctx context.Context, local string, plan helperPlan) (map[int]uint64, error) {
	f, err := os.Open(local)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec, err := zstd.NewReader(ctxReader{ctx, f}, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	needs := map[int]uint64{}
	tr := tar.NewReader(dec)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return needs, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			return nil, fmt.Errorf("the restore point's archive can't be read: %w", err)
		}
		name := strings.TrimSuffix(h.Name, "/")
		best := -1
		for i, p := range plan.Paths {
			if (p.Entry == "" || name == p.Entry || strings.HasPrefix(name, p.Entry+"/")) &&
				(best < 0 || len(p.Entry) > len(plan.Paths[best].Entry)) {
				best = i
			}
		}
		if best < 0 {
			continue // the helper refuses it
		}
		const block = 4096
		size := uint64(block)
		if h.Typeflag == tar.TypeReg {
			size = (uint64(max(h.Size, 0)) + block - 1) / block * block
		}
		needs[plan.Paths[best].Mount] += size
	}
}

// restoreArgs is the `docker run` of one restore: the backup's sources
// mounted read-write at /src/<n>, the agent's binary at /wf, the copy on
// stdin (-i).
func restoreArgs(name, appID, exe string, plan helperPlan, b recipe.EffectiveBackup, needs map[int]uint64) []string {
	args := []string{"run", "--rm", "-i", "--name", name}
	args = append(args, restoreLimits...)
	args = append(args, "--label", helper.RoleLabel, "--label", helper.AppLabel+"="+appID)
	for i, m := range plan.Mounts {
		spec := "type=volume," + csvField("source="+m.Volume)
		if m.Bind != "" {
			spec = "type=bind," + csvField("source="+m.Bind)
		}
		args = append(args, "--mount", spec+",target=/src/"+strconv.Itoa(i))
	}
	args = append(args, "--mount", "type=bind,"+csvField("source="+exe)+",target=/wf,readonly", helper.Image, "/wf", "backup", "restore")
	if b.Type == recipe.TypeSQLite {
		return append(args, "sqlite", plan.Paths[0].Src(), "--entry", plan.Paths[0].Entry)
	}
	args = append(args, "files", "--id", strings.TrimPrefix(name, "wf-restore-"))
	for _, p := range plan.Paths {
		args = append(args, "--root", "/src/"+strconv.Itoa(p.Mount), "--src", p.Src(), "--entry", p.Entry)
	}
	// What the backup excludes is not in the copy; the helper keeps it where
	// it is instead of deleting it with the old data.
	for _, e := range b.Exclude {
		args = append(args, "--exclude", e)
	}
	for i := range plan.Mounts {
		if n := needs[i]; n > 0 {
			args = append(args, "--need", "/src/"+strconv.Itoa(i)+"="+strconv.FormatUint(n, 10))
		}
	}
	return args
}

// runZstdRestore runs the restore helper with the decompressed copy on its
// stdin.
func (r *Repository) runZstdRestore(ctx context.Context, name string, args []string, local string) (int, []byte, error) {
	f, err := os.Open(local)
	if err != nil {
		return -1, nil, fmt.Errorf("%w: %w", errHelperNotStarted, err)
	}
	defer f.Close()
	dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return -1, nil, fmt.Errorf("%w: %w", errHelperNotStarted, err)
	}
	defer dec.Close()
	return r.runRestoreHelper(ctx, name, args, dec)
}

// ctxReader stops a copy once ctx is done.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
