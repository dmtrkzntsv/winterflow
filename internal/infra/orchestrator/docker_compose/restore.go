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

// restoreLimits are a restore helper's: a dump's, plus what putting files
// back needs (give them their owner; set the mode and times of files it
// doesn't own; keep a folder's setgid bit when its group is set).
var restoreLimits = append(slices.Clone(helperLimits), "--cap-add", "CHOWN", "--cap-add", "FOWNER", "--cap-add", "FSETID")

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
//  1. the point is downloaded and its sha256 checked against its manifest
//     (a mismatch aborts before anything is stopped);
//  2. a running app is stopped (compose stop);
//  3. the current data is saved as a pre-restore point (a normal run of
//     kind pre-restore); when that fails the app is started again and
//     nothing is restored (nothing to save — no database file, none of the
//     paths — skips it);
//  4. the helper puts the copy back (sqlite: replaces the file; files:
//     extracts beside the data, checking free space, then swaps);
//  5. the app is started again.
//
// On any failure that leaves the data as it was, the app is started again
// (unless the restore was stopped because the app is being deleted). A
// files restore that failed half-way through its swap leaves the app
// stopped, and the error names the pre-restore point to go back to. ctx
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
		if needs, err = archiveNeeds(local, plan); err != nil {
			return resp, err
		}
	}

	psCtx, cancel := context.WithTimeout(ctx, dockerCallTimeout)
	up, err := r.runningProjects(psCtx)
	cancel()
	if err != nil {
		return resp, fmt.Errorf("check whether the app is running: %w", err)
	}
	running := up[projectName(job.AppID)]
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

	pre, err := r.RunBackup(ctx, BackupJob{AppID: job.AppID, Backup: b.Name, Kind: command.PointPreRestore})
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

	name := "wf-restore-" + strconv.FormatInt(r.now().UnixNano(), 36)
	args := restoreArgs(name, job.AppID, exe, plan, b, needs)
	exit, stderr, err := r.runZstdRestore(ctx, name, args, local)
	res, parsed := helper.ParseResult(stderr)
	resp.Warnings = append(resp.Warnings, res.Warnings...)
	back := "the copy saved before it"
	if resp.PreRestorePoint != "" {
		back = "the pre-restore point " + resp.PreRestorePoint
	}
	switch {
	case err == nil && exit == helper.ExitOK && parsed:
	case err == nil && exit == helper.ExitPartial:
		return resp, fmt.Errorf("the restore stopped half-way: %s. The app was left stopped; restore %s to go back", reason(res, stderr, exit), back)
	case err != nil && b.Type == recipe.TypeFiles:
		// Stopped from outside (its time limit): it may have been swapping.
		return resp, fmt.Errorf("the restore helper was stopped (%w), so the paths may be partly replaced. The app was left stopped; restore %s to go back", err, back)
	case err != nil:
		// A database is swapped by one rename: it is the old or the restored one.
		return resp, fail(fmt.Errorf("the restore helper was stopped (%w): the database is the old one or the restored one", err))
	default:
		return resp, fail(fmt.Errorf("the restore failed, the app's data is unchanged: %s", reason(res, stderr, exit)))
	}
	if running {
		if err := r.startAgain(ctx, job.AppID, nil); err != nil {
			resp.Warnings = append(resp.Warnings, "the data was restored, but the app did not start again: "+err.Error())
		}
	}
	return resp, nil
}

// reason is why a helper failed: its result's error, else its exit code and
// last lines.
func reason(res helper.Result, stderr []byte, exit int) string {
	if res.Error != "" {
		return res.Error
	}
	return fmt.Sprintf("the helper exited with %d: %s", exit, lastLines(stderr, 3))
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
	n, err := io.Copy(io.MultiWriter(f, sum), rc)
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
func archiveNeeds(local string, plan helperPlan) (map[int]uint64, error) {
	f, err := os.Open(local)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
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
		return -1, nil, err
	}
	defer f.Close()
	dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return -1, nil, err
	}
	defer dec.Close()
	return r.runRestoreHelper(ctx, name, args, dec)
}
