package dockercompose

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/helper"
	"winterflow/internal/infra/backup/layout"
)

// fakeRestorer stands in for the restore helper's `docker run`: it records
// the arguments and stdin, and answers exit with stderr (a result line by
// default). onRun runs first.
type fakeRestorer struct {
	mu     sync.Mutex
	calls  [][]string
	stdin  []byte
	exit   int
	stderr string
	onRun  func()
}

func (f *fakeRestorer) run(_ context.Context, _ string, args []string, in io.Reader) (int, []byte, error) {
	body, err := io.ReadAll(in)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls, f.stdin = append(f.calls, args), body
	if f.onRun != nil {
		f.onRun()
	}
	if err != nil {
		return 1, nil, err
	}
	if f.stderr == "" {
		return f.exit, []byte(`{"raw_size":1,"warnings":["/app/media/dev: skipped"]}` + "\n"), nil
	}
	return f.exit, []byte(f.stderr), nil
}

// restoreRepo is a backupRepo whose app is up, with compose calls recorded
// and a fake restore helper.
type restoreRepo struct {
	*backupRepo
	restorer *fakeRestorer
	compose  []string
	up       bool
}

func newRestoreRepo(t *testing.T) *restoreRepo {
	t.Helper()
	rr := &restoreRepo{backupRepo: newBackupRepo(t), restorer: &fakeRestorer{}, up: true}
	rr.runRestoreHelper = rr.restorer.run
	rr.runningProjects = func(context.Context) (map[string]bool, error) {
		return map[string]bool{"wf-app-1": rr.up}, nil
	}
	rr.runCompose = func(_ context.Context, _ string, args []string) error {
		rr.compose = append(rr.compose, composeVerb(args))
		return nil
	}
	return rr
}

// composeVerb is the compose command of a recorded argument list ("down" is
// followed by its flags).
func composeVerb(args []string) string {
	if slices.Contains(args, "down") {
		return "down"
	}
	return args[len(args)-1]
}

// filesPoint saves a files archive (one folder) as the media backup's data
// and makes a point of it.
func (rr *restoreRepo) filesPoint(t *testing.T) string {
	t.Helper()
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	_ = tw.WriteHeader(&tar.Header{Name: "app/media/", Typeflag: tar.TypeDir, Mode: 0o755})
	_ = tw.Close()
	rr.helper.payload, rr.helper.fp = archive.String(), "files:"+strconv.Itoa(len(rr.stored(t)))
	return rr.point(t, "media")
}

// point makes a copy of name now and moves the clock on an hour.
func (rr *restoreRepo) point(t *testing.T, name string) string {
	t.Helper()
	res, err := rr.run(t, name, command.PointScheduled)
	if err != nil || res.Point == nil {
		t.Fatalf("backup: %+v, %v", res, err)
	}
	rr.clock = rr.clock.Add(time.Hour)
	return res.Point.ID
}

func (rr *restoreRepo) restore(ctx context.Context, name, point string) (command.RestoreBackupResponse, error) {
	return rr.RestoreBackup(ctx, RestoreJob{AppID: "app-1", Backup: name, Point: point})
}

// tmpLeft lists what remains in backups/tmp.
func (rr *restoreRepo) tmpLeft() []string {
	entries, _ := os.ReadDir(rr.restoreTmpDir())
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestRestoreSQLitePoint(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	rr.helper.payload = "NEWER-SNAPSHOT" // what the pre-restore copy saves
	rr.helper.fp = "sqlite:2"
	resp, err := rr.restore(context.Background(), "database", id)
	if err != nil {
		t.Fatal(err)
	}
	if resp.RestoredPoint != id || resp.PreRestorePoint != "20261011T130000Z-pre-restore" || resp.AppID != "app-1" || resp.Backup != "database" ||
		!slices.Equal(resp.Warnings, []string{"the pre-restore copy: a note", "/app/media/dev: skipped"}) {
		t.Fatalf("response = %+v", resp)
	}
	if !slices.Equal(rr.compose, []string{"stop", "start"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}
	if string(rr.restorer.stdin) != "SQLITE-SNAPSHOT" {
		t.Fatalf("the helper read %q", rr.restorer.stdin)
	}
	args := strings.Join(rr.restorer.calls[0], " ")
	for _, part := range []string{
		"run --rm -i --name wf-restore-",
		"--cap-drop ALL --cap-add DAC_OVERRIDE --cap-add SETUID --cap-add SETGID --security-opt no-new-privileges --cap-add CHOWN --cap-add FOWNER --cap-add FSETID",
		"--label winterflow.role=backup-helper --label winterflow.app=app-1",
		"--mount type=volume,source=wf-app-1_data,target=/src/0 --mount",
		"alpine:3.20 /wf backup restore sqlite /src/0/app.db --entry app.db",
	} {
		if !strings.Contains(args, part) {
			t.Errorf("args lack %q:\n%s", part, args)
		}
	}
	if !slices.Contains(rr.stored(t), "app-1/database/20261011T130000Z-pre-restore.json") {
		t.Fatalf("no pre-restore point: %v", rr.stored(t))
	}
	if left := rr.tmpLeft(); len(left) != 0 {
		t.Fatalf("downloads left: %v", left)
	}
}

// A stopped app stays stopped: nothing to stop, nothing to start.
func TestRestoreLeavesAStoppedAppStopped(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	rr.up = false
	if _, err := rr.restore(context.Background(), "database", id); err != nil {
		t.Fatal(err)
	}
	if len(rr.compose) != 0 {
		t.Fatalf("compose calls = %v", rr.compose)
	}
}

// A copy whose data doesn't match its manifest is refused before the app
// is touched.
func TestRestoreRefusesADamagedPoint(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	data := filepath.Join(rr.disk, "app-1", "database", id+".db.zst")
	raw, _ := os.ReadFile(data)
	raw[len(raw)-1] ^= 0xff
	if err := os.WriteFile(data, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := rr.restore(context.Background(), "database", id)
	if err == nil || !strings.Contains(err.Error(), "is damaged: its data does not match its manifest") {
		t.Fatalf("err = %v", err)
	}
	if len(rr.compose) != 0 || len(rr.restorer.calls) != 0 || len(rr.tmpLeft()) != 0 {
		t.Fatalf("compose %v, helper %d, tmp %v", rr.compose, len(rr.restorer.calls), rr.tmpLeft())
	}
}

func TestRestoreRefusals(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	for name, tc := range map[string]struct {
		backup, point, want string
	}{
		"unknown point":  {"database", "20200101T000000Z-manual", "has no restore point 20200101T000000Z-manual"},
		"backup off":     {"replica", id, "the backup replica is off"},
		"unknown backup": {"nope", id, "the app has no backup nope"},
	} {
		if _, err := rr.restore(context.Background(), tc.backup, tc.point); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	if _, err := rr.RestoreBackup(context.Background(), RestoreJob{AppID: "gone", Backup: "database", Point: id}); !errors.Is(err, ErrNotRunnable) {
		t.Fatalf("unknown app: %v", err)
	}
	// A point whose manifest names another type (the backup changed type).
	mpath := filepath.Join(rr.disk, "app-1", "database", id+".json")
	raw, _ := os.ReadFile(mpath)
	var m layout.Manifest
	_ = json.Unmarshal(raw, &m)
	m.Type = "files"
	raw, _ = json.Marshal(m)
	if err := os.WriteFile(mpath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rr.restore(context.Background(), "database", id); err == nil || !strings.Contains(err.Error(), "is a files copy, but the backup database is a sqlite backup now") {
		t.Fatalf("type change: %v", err)
	}
	if len(rr.compose) != 0 {
		t.Fatalf("compose calls = %v", rr.compose)
	}
}

// A failed pre-restore copy restores nothing and starts the app again;
// nothing to save (no database file yet) only warns.
func TestRestorePreRestoreCopy(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	rr.helper.exit, rr.helper.stderr = helper.ExitFailed, `{"error":"the copy failed its integrity check"}`+"\n"
	_, err := rr.restore(context.Background(), "database", id)
	if err == nil || !strings.Contains(err.Error(), "the pre-restore copy failed, so nothing was restored: the copy failed its integrity check") {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(rr.compose, []string{"stop", "start"}) || len(rr.restorer.calls) != 0 {
		t.Fatalf("compose %v, helper %d", rr.compose, len(rr.restorer.calls))
	}

	rr.compose = nil
	rr.helper.exit, rr.helper.stderr = helper.ExitMissing, `{"error":"database file not found: /src/0/app.db"}`+"\n"
	resp, err := rr.restore(context.Background(), "database", id)
	if err != nil || resp.PreRestorePoint != "" || !slices.Contains(resp.Warnings, "nothing was saved before the restore: database file not found: /src/0/app.db") {
		t.Fatalf("missing database: %+v, %v", resp, err)
	}
	if !slices.Equal(rr.compose, []string{"stop", "start"}) || len(rr.restorer.calls) != 1 {
		t.Fatalf("compose %v, helper %d", rr.compose, len(rr.restorer.calls))
	}
}

// A helper that fails before swapping leaves the data as it was: the app is
// started again. One that fails while swapping leaves the app stopped and
// names the pre-restore point.
func TestRestoreHelperFailures(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	rr.restorer.exit, rr.restorer.stderr = helper.ExitFailed, `{"error":"the copy can't be restored: file is not a database"}`+"\n"
	_, err := rr.restore(context.Background(), "database", id)
	if err == nil || err.Error() != "the restore failed, the app's data is unchanged: the copy can't be restored: file is not a database" {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(rr.compose, []string{"stop", "start"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}

	rr.compose = nil
	rr.clock = rr.clock.Add(time.Hour)
	rr.restorer.exit, rr.restorer.stderr = helper.ExitPartial, `{"error":"/app/media was partly replaced: permission denied"}`+"\n"
	_, err = rr.restore(context.Background(), "database", id)
	if err == nil || !strings.Contains(err.Error(), "the restore stopped half-way: /app/media was partly replaced: permission denied. The app was left stopped; restore the pre-restore point 20261011T140000Z-pre-restore to go back") {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(rr.compose, []string{"stop", "down"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}

	// Docker itself failing, and a helper with no result line.
	rr.compose = nil
	rr.clock = rr.clock.Add(time.Hour)
	rr.restorer.exit, rr.restorer.stderr = 125, "docker: Error response from daemon\n"
	if _, err := rr.restore(context.Background(), "database", id); err == nil || !strings.Contains(err.Error(), "the helper exited with 125: docker: Error response from daemon") {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(rr.compose, []string{"stop", "start"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}
}

// A helper stopped from outside (its time limit) may have been swapping: the
// app is left stopped and removed (compose down), naming the pre-restore
// point, whatever the type.
func TestRestoreHelperStopped(t *testing.T) {
	rr := newRestoreRepo(t)
	stopped := errors.New("stopped after its time limit: context deadline exceeded")
	rr.runRestoreHelper = func(_ context.Context, _ string, _ []string, in io.Reader) (int, []byte, error) {
		_, _ = io.Copy(io.Discard, in)
		return -1, nil, stopped
	}
	id := rr.point(t, "database")
	_, err := rr.restore(context.Background(), "database", id)
	if !errors.Is(err, stopped) || !strings.Contains(err.Error(), "the database file may be partly replaced. The app was left stopped; restore the pre-restore point 20261011T130000Z-pre-restore to go back") {
		t.Fatalf("sqlite: %v", err)
	}
	if !slices.Equal(rr.compose, []string{"stop", "down"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}
	rr.compose = nil
	id = rr.filesPoint(t)
	_, err = rr.restore(context.Background(), "media", id)
	if !errors.Is(err, stopped) || !strings.Contains(err.Error(), "the paths may be partly replaced. The app was left stopped; restore the pre-restore point 20261011T140000Z-pre-restore to go back") ||
		!strings.Contains(err.Error(), "stays in a .wf-restore-*.old folder at the root of the mount: move or remove it before restoring again") {
		t.Fatalf("files: %v", err)
	}
	if !slices.Equal(rr.compose, []string{"stop", "down"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}
}

// Only a clean failure (exit 1) or docker not running the helper at all
// leaves the data as it was. A crash, a kill, a swap that failed or a
// "success" with no result leave the app stopped and removed, for both
// types; an app that was not running has nothing to remove.
func TestRestoreUnexpectedHelperEnds(t *testing.T) {
	for name, tc := range map[string]struct {
		exit       int
		stderr     string
		unchanged  bool
		wantReason string
	}{
		"clean failure": {helper.ExitFailed, `{"error":"no room"}` + "\n", true, "the app's data is unchanged: no room"},
		"docker 125":    {125, "docker: daemon\n", true, "the app's data is unchanged"},
		"docker 126":    {126, "cannot execute\n", true, "the app's data is unchanged"},
		"docker 127":    {127, "not found\n", true, "the app's data is unchanged"},
		"panic":         {2, "panic: boom\n", false, "the restore helper ended unexpectedly (the helper exited with 2: panic: boom)"},
		"killed":        {137, "", false, "the restore helper ended unexpectedly (the helper exited with 137"},
		"terminated":    {143, "", false, "the restore helper ended unexpectedly (the helper exited with 143"},
		"no result":     {helper.ExitOK, "done\n", false, "the restore helper ended unexpectedly (the helper exited with 0: done)"},
	} {
		for _, files := range []bool{false, true} {
			rr := newRestoreRepo(t)
			var id, backup string
			if files {
				id, backup = rr.filesPoint(t), "media"
			} else {
				id, backup = rr.point(t, "database"), "database"
			}
			rr.restorer.exit, rr.restorer.stderr = tc.exit, tc.stderr
			if tc.stderr == "" {
				rr.restorer.stderr = "killed\n"
			}
			_, err := rr.restore(context.Background(), backup, id)
			want := []string{"stop", "down"}
			if tc.unchanged {
				want = []string{"stop", "start"}
			}
			label := name + "/" + backup
			if err == nil || !strings.Contains(err.Error(), tc.wantReason) || !slices.Equal(rr.compose, want) {
				t.Errorf("%s: err = %v, compose = %v, want %v", label, err, rr.compose, want)
			}
			if !tc.unchanged && !strings.Contains(err.Error(), "The app was left stopped; restore the pre-restore point ") {
				t.Errorf("%s: no pointer to the pre-restore point: %v", label, err)
			}
			// An app that was not running: nothing was stopped, nothing to remove.
			rr.up, rr.compose = false, nil
			rr.clock = rr.clock.Add(time.Hour)
			_, _ = rr.restore(context.Background(), backup, id)
			if len(rr.compose) != 0 {
				t.Errorf("%s, stopped app: compose calls = %v", label, rr.compose)
			}
		}
	}
}

// With nothing to save first (the paths don't exist yet), a left-stopped
// error says there is no copy to go back to, and a failed compose down is
// named.
func TestRestoreLeftStoppedDetails(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.filesPoint(t)
	rr.helper.exit, rr.helper.stderr = helper.ExitMissing, `{"error":"none of the paths exist"}`+"\n"
	rr.restorer.exit, rr.restorer.stderr = helper.ExitPartial, `{"error":"/app/media was partly replaced"}`+"\n"
	rr.runCompose = func(_ context.Context, _ string, args []string) error {
		rr.compose = append(rr.compose, composeVerb(args))
		if composeVerb(args) == "down" {
			return errors.New("daemon busy")
		}
		return nil
	}
	_, err := rr.restore(context.Background(), "media", id)
	if err == nil || !strings.Contains(err.Error(), "no copy was saved before the restore (there was nothing to save), so there is nothing to go back to") ||
		!strings.Contains(err.Error(), "removing the app's containers failed, so Docker may start them again on this data: daemon busy") ||
		!slices.Equal(rr.compose, []string{"stop", "down"}) {
		t.Fatalf("err = %v, compose = %v", err, rr.compose)
	}
}

// A restore stopped because its app is being deleted doesn't start it
// again; a start that fails is a warning once the data is back.
func TestRestoreStartAgain(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	rr.restorer.exit, rr.restorer.stderr = helper.ExitFailed, `{"error":"stopped"}`+"\n"
	rr.restorer.onRun = func() { cancel(ErrAppDeleted) }
	if _, err := rr.restore(ctx, "database", id); err == nil {
		t.Fatal("no error")
	}
	if !slices.Equal(rr.compose, []string{"stop"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}

	rr.compose, rr.restorer.onRun = nil, nil
	rr.restorer.exit, rr.restorer.stderr = 0, ""
	rr.runCompose = func(_ context.Context, _ string, args []string) error {
		rr.compose = append(rr.compose, composeVerb(args))
		if composeVerb(args) == "start" {
			return errors.New("port is already allocated")
		}
		return nil
	}
	resp, err := rr.restore(context.Background(), "database", id)
	if err != nil || !slices.Contains(resp.Warnings, "the data was restored, but the app did not start again: start the app again: port is already allocated") {
		t.Fatalf("%+v, %v", resp, err)
	}
	// A failed stop is reported and the app started again.
	rr.compose = nil
	rr.runCompose = func(_ context.Context, _ string, args []string) error {
		rr.compose = append(rr.compose, composeVerb(args))
		if composeVerb(args) == "stop" {
			return errors.New("daemon busy")
		}
		return nil
	}
	if _, err := rr.restore(context.Background(), "database", id); err == nil || err.Error() != "stop the app: daemon busy" {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(rr.compose, []string{"stop", "start"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}
}

// A files restore names its mounts, paths, the backup's excludes (the helper
// keeps what they match) and the room each mount needs.
func TestRestoreFilesPoint(t *testing.T) {
	rr := newRestoreRepo(t)
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	for _, h := range []*tar.Header{
		{Name: "app/media/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "app/media/a.jpg", Typeflag: tar.TypeReg, Mode: 0o644, Size: 5000},
		{Name: "app/media/b.jpg", Typeflag: tar.TypeReg, Mode: 0o644, Size: 10},
	} {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(make([]byte, h.Size)); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	rr.helper.payload, rr.helper.fp = archive.String(), "files:1"
	id := rr.point(t, "media")
	if _, err := rr.restore(context.Background(), "media", id); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(rr.restorer.calls[0], " ")
	if !strings.Contains(args, "--mount type=bind,source=/data/apps-data/app-1/media,target=/src/0 --mount") ||
		!strings.Contains(args, "/wf backup restore files --id ") ||
		!strings.HasSuffix(args, "--root /src/0 --src /src/0 --entry app/media --exclude *.tmp --need /src/0=16384") {
		t.Fatalf("args = %s", args)
	}
	if !bytes.Equal(rr.restorer.stdin, archive.Bytes()) {
		t.Fatal("the helper did not get the archive")
	}
	// An archive that can't be read is refused before the app stops.
	rr.compose = nil
	rr.helper.fp = "files:2"
	rr.helper.payload = "not a tar archive, just text long enough to be a header block for tar reading purposes ........" + strings.Repeat(".", 500)
	id = rr.point(t, "media")
	if _, err := rr.restore(context.Background(), "media", id); err == nil || !strings.Contains(err.Error(), "archive can't be read") {
		t.Fatalf("err = %v", err)
	}
	if len(rr.compose) != 0 {
		t.Fatalf("compose calls = %v", rr.compose)
	}
}

// Downloads left by an agent that stopped mid-restore go at the next start.
func TestRemoveRestoreDownloads(t *testing.T) {
	r := newTestRepo(t)
	writeFileT(t, r.restoreTmpDir(), "20261011T120000Z-manual-abc.zst", "partial")
	r.removeRestoreDownloads()
	if _, err := os.Stat(r.restoreTmpDir()); !os.IsNotExist(err) {
		t.Fatalf("backups/tmp is still there: %v", err)
	}
}

// A restore stopped because its app is being deleted removes nothing and
// starts nothing, whatever the helper's end.
func TestRestoreAppDeletedLeavesContainersAlone(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.filesPoint(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	rr.restorer.exit, rr.restorer.stderr = 137, "killed\n"
	rr.restorer.onRun = func() { cancel(ErrAppDeleted) }
	if _, err := rr.restore(ctx, "media", id); err == nil {
		t.Fatal("no error")
	}
	if !slices.Equal(rr.compose, []string{"stop"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}
}

// While a restore runs, the app's lifecycle operations are refused, so
// nothing starts containers on the data being replaced; a delete waits for
// the restore and then proceeds. Once it ends, they work again.
func TestRestoreRefusesLifecycleOperations(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	rr.restoreWait = 50 * time.Millisecond
	ctx := context.Background()
	var inside map[string]error
	rr.restorer.onRun = func() {
		_, _, saveErr := rr.SaveAppResult(ctx, command.AppPayload{AppID: "app-1"}, true)
		_, rollbackErr := rr.Rollback(ctx, "app-1", "abc")
		_, backupsErr := rr.UpdateBackups(ctx, "app-1", nil, "")
		_, deleteErr := rr.DeleteApp(ctx, "app-1", false, false)
		inside = map[string]error{
			"start": rr.StartApp(ctx, "app-1"), "stop": rr.StopApp(ctx, "app-1"), "restart": rr.RestartApp(ctx, "app-1"),
			"update": rr.UpdateApp(ctx, "app-1"), "save": saveErr, "rollback": rollbackErr,
			"rename": rr.RenameApp(ctx, "app-1", "x"), "backups": backupsErr, "delete": deleteErr,
			"deploy": rr.deploy(ctx, "app-1"), "composeUp": rr.composeUp(ctx, "app-1"), "composeRestart": rr.composeRestart(ctx, "app-1"),
			"restore": func() error { _, err := rr.restore(ctx, "database", id); return err }(),
		}
	}
	if _, err := rr.restore(ctx, "database", id); err != nil {
		t.Fatal(err)
	}
	for name, err := range inside {
		if !errors.Is(err, ErrRestoring) {
			t.Errorf("%s during a restore: %v", name, err)
		}
	}
	if len(inside) != 13 {
		t.Fatalf("operations checked: %d", len(inside))
	}
	if !rr.appExists("app-1") {
		t.Fatal("the app was deleted during its restore")
	}
	rr.compose = nil
	if err := rr.StopApp(ctx, "app-1"); err != nil || len(rr.compose) != 1 {
		t.Fatalf("after the restore: %v, %v", err, rr.compose)
	}
	// The mark is cleared when a restore fails too.
	rr.restorer.onRun, rr.restorer.exit, rr.restorer.stderr = nil, helper.ExitFailed, `{"error":"x"}`+"\n"
	rr.clock = rr.clock.Add(time.Hour)
	_, _ = rr.restore(ctx, "database", id)
	if rr.restoreRunning("app-1") {
		t.Fatal("still marked as restoring")
	}
}

func TestDeleteWaitsForTheRestore(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	started, release := make(chan struct{}), make(chan struct{})
	rr.restorer.onRun = func() {
		close(started)
		<-release
	}
	restored := make(chan error, 1)
	go func() {
		_, err := rr.restore(context.Background(), "database", id)
		restored <- err
	}()
	<-started
	deleted := make(chan error, 1)
	go func() {
		_, err := rr.DeleteApp(context.Background(), "app-1", false, false)
		deleted <- err
	}()
	select {
	case err := <-deleted:
		t.Fatalf("the delete ran beside the restore: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if !rr.appExists("app-1") {
		t.Fatal("the app is gone")
	}
	close(release)
	if err := <-restored; err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if rr.appExists("app-1") {
		t.Fatal("the delete did not run after the restore")
	}
}

// A source poll skips an app being restored and polls it when it is over;
// a storage redeploy names it in a warning instead.
func TestRestoreSkipsPollsAndRedeploys(t *testing.T) {
	r := newTestRepo(t)
	up, commit := newUpstream(t)
	commit("f", "v1\n")
	if _, err := r.saveWithoutDeploy(sourcePayload(up, "")); err != nil {
		t.Fatal(err)
	}
	before, _ := readSourceLock(r.appDataDir("git-app"))
	end, err := r.beginRestore("git-app")
	if err != nil {
		t.Fatal(err)
	}
	commit("f", "v2\n")
	for i := 0; i < 3; i++ {
		if ids := r.RefreshDueSources(context.Background()); len(ids) != 0 {
			t.Fatalf("polled during the restore: %v", ids)
		}
		timeSleepShim()
	}
	if lock, _ := readSourceLock(r.appDataDir("git-app")); lock.SHA != before.SHA {
		t.Fatal("the pin moved during the restore")
	}
	r.runningProjects = func(context.Context) (map[string]bool, error) { return map[string]bool{"wf-git-app": true}, nil }
	redeployed, warnings := r.redeployStorageUsers(context.Background(), []string{"git-app"})
	if len(redeployed) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "is being restored and was not redeployed") {
		t.Fatalf("redeployed %v, warnings %v", redeployed, warnings)
	}
	end()
	if _, err := r.beginRestore("git-app"); err != nil {
		t.Fatalf("the mark is not cleared: %v", err)
	}
}

// Deleting an app forgets a restore mark left behind.
func TestForgetAppClearsTheRestoreMark(t *testing.T) {
	r := newTestRepo(t)
	end, err := r.beginRestore("a")
	if err != nil {
		t.Fatal(err)
	}
	r.forgetApp("a")
	end() // must not panic, and a new restore may begin
	end2, err := r.beginRestore("a")
	if err != nil {
		t.Fatal(err)
	}
	end2()
}

// A download larger than its manifest says is cut off one byte past it, and
// one the disk has no room for is refused before any byte is written.
func TestRestoreDownloadBounds(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	mpath := filepath.Join(rr.disk, "app-1", "database", id+".json")
	raw, _ := os.ReadFile(mpath)
	var m layout.Manifest
	_ = json.Unmarshal(raw, &m)
	real := m.Size
	m.Size = 3
	raw, _ = json.Marshal(m)
	if err := os.WriteFile(mpath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := rr.restore(context.Background(), "database", id)
	if err == nil || !strings.Contains(err.Error(), "is damaged") || !strings.Contains(err.Error(), "(4 bytes") {
		t.Fatalf("err = %v", err)
	}
	if left := rr.tmpLeft(); len(left) != 0 || len(rr.compose) != 0 {
		t.Fatalf("tmp %v, compose %v", left, rr.compose)
	}

	m.Size = real
	raw, _ = json.Marshal(m)
	_ = os.WriteFile(mpath, raw, 0o600)
	rr.diskFree = func(string) (uint64, error) { return uint64(real) + restoreDownloadMargin - 1, nil }
	_, err = rr.restore(context.Background(), "database", id)
	if err == nil || !strings.Contains(err.Error(), "not enough free space to download the restore point") || len(rr.tmpLeft()) != 0 || len(rr.compose) != 0 {
		t.Fatalf("err = %v, tmp %v, compose %v", err, rr.tmpLeft(), rr.compose)
	}
	rr.diskFree = func(string) (uint64, error) { return uint64(real) + restoreDownloadMargin, nil }
	if _, err := rr.restore(context.Background(), "database", id); err != nil {
		t.Fatalf("with exactly the room: %v", err)
	}
}

// Reading an archive's headers stops once the restore's context is done.
func TestArchiveNeedsStopsWithTheContext(t *testing.T) {
	rr := newRestoreRepo(t)
	rr.filesPoint(t)
	var local string
	_ = filepath.WalkDir(rr.disk, func(p string, d os.DirEntry, _ error) error {
		if strings.HasSuffix(p, ".tar.zst") || (strings.Contains(p, "media") && strings.HasSuffix(p, ".zst")) {
			local = p
		}
		return nil
	})
	if local == "" {
		t.Fatal("no archive stored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := archiveNeeds(ctx, local, helperPlan{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

// With too little of the time budget left, nothing is stopped.
func TestRestoreNeedsTimeLeft(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, err := rr.restore(ctx, "database", id)
	if err == nil || !strings.Contains(err.Error(), "too little time is left to restore safely") || len(rr.compose) != 0 || len(rr.restorer.calls) != 0 {
		t.Fatalf("err = %v, compose %v", err, rr.compose)
	}
	ctx, cancel2 := context.WithTimeout(context.Background(), time.Hour)
	defer cancel2()
	if _, err := rr.restore(ctx, "database", id); err != nil {
		t.Fatal(err)
	}
}

// The pre-restore run's retention keeps the point being restored, even one
// past the ceiling.
func TestRestoreKeepsItsPointThroughPruning(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	rr.clock = rr.clock.Add(200 * 24 * time.Hour) // past the 180-day ceiling
	if _, err := rr.restore(context.Background(), "database", id); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(rr.stored(t), "app-1/database/"+id+".json") {
		t.Fatalf("the restored point was pruned: %v", rr.stored(t))
	}
}

// An app with undeployed changes may have containers HEAD's compose files
// don't know: it is refused while running, and fine while stopped.
func TestRestoreRefusesARunningAppWithADraft(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	rr.markDeployed("app-1", "0000000000000000000000000000000000000000")
	_, err := rr.restore(context.Background(), "database", id)
	if err == nil || !strings.Contains(err.Error(), "the app has undeployed changes") || len(rr.compose) != 0 || rr.restoreRunning("app-1") {
		t.Fatalf("err = %v, compose %v", err, rr.compose)
	}
	rr.up = false
	if _, err := rr.restore(context.Background(), "database", id); err != nil {
		t.Fatalf("stopped app: %v", err)
	}
}

// A restore does not begin while an operation that may start containers is
// running (it passed its check before the restore's mark existed); the
// restore is refused cleanly and works once the operation is over.
func TestRestoreRefusedWhileAnOperationRuns(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		verb string
		op   func(rr *restoreRepo) error
	}{
		"start":   {"up", func(rr *restoreRepo) error { return rr.StartApp(ctx, "app-1") }},
		"restart": {"restart", func(rr *restoreRepo) error { return rr.RestartApp(ctx, "app-1") }},
		"save": {"up", func(rr *restoreRepo) error {
			_, _, err := rr.SaveAppResult(ctx, command.AppPayload{AppID: "app-1", Config: []byte(plainConfig),
				Files: []command.ContentItem{{Name: "compose.yml", Content: []byte(backupCompose)}}}, false)
			return err
		}},
	} {
		rr := newRestoreRepo(t)
		id := rr.point(t, "database")
		inside, release := make(chan struct{}), make(chan struct{})
		rr.runCompose = func(_ context.Context, _ string, args []string) error {
			if composeVerb(args) == tc.verb || slices.Contains(args, tc.verb) {
				select {
				case <-inside:
				default:
					close(inside)
				}
				<-release
			}
			rr.compose = append(rr.compose, composeVerb(args))
			return nil
		}
		done := make(chan error, 1)
		go func() { done <- tc.op(rr) }()
		select {
		case <-inside:
		case err := <-done:
			t.Fatalf("%s ended before reaching compose: %v", name, err)
		case <-time.After(5 * time.Second):
			t.Fatalf("%s never reached compose", name)
		}
		_, err := rr.restore(ctx, "database", id)
		if !errors.Is(err, ErrAppBusy) || !strings.Contains(err.Error(), "the app is busy (an operation on it, such as a deploy, is still running); try again") ||
			len(rr.restorer.calls) != 0 || rr.restoreRunning("app-1") {
			t.Errorf("%s: err = %v, helper calls %d", name, err, len(rr.restorer.calls))
		}
		close(release)
		<-done
		rr.compose = nil
		if _, err := rr.restore(ctx, "database", id); err != nil {
			t.Errorf("%s: after the operation: %v", name, err)
		}
	}
}

// A left-stopped app is removed with compose down and never with -v: its
// volumes are the data the user may go back to.
func TestRestoreLeftStoppedNeverRemovesVolumes(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	var down []string
	rr.runCompose = func(_ context.Context, _ string, args []string) error {
		rr.compose = append(rr.compose, composeVerb(args))
		if composeVerb(args) == "down" {
			down = args
		}
		return nil
	}
	rr.restorer.exit, rr.restorer.stderr = 137, "killed\n"
	if _, err := rr.restore(context.Background(), "database", id); err == nil {
		t.Fatal("no error")
	}
	if down == nil {
		t.Fatalf("no compose down: %v", rr.compose)
	}
	for _, a := range down {
		if a == "-v" || a == "--volumes" {
			t.Fatalf("down removes volumes: %v", down)
		}
	}
}

// A rename changes nothing that runs: a deployed app stays deployed (so a
// restore is not refused for "undeployed changes"), a draft stays a draft.
func TestRenameKeepsADeployedAppDeployed(t *testing.T) {
	rr := newRestoreRepo(t)
	rr.markDeployed("app-1", "")
	if rr.hasDraft("app-1") {
		t.Fatal("a deployed app has a draft")
	}
	if err := rr.RenameApp(context.Background(), "app-1", "renamed"); err != nil {
		t.Fatal(err)
	}
	if rr.hasDraft("app-1") {
		t.Fatal("the rename made the app a draft")
	}
	id := rr.point(t, "database")
	if _, err := rr.restore(context.Background(), "database", id); err != nil {
		t.Fatalf("restore after a rename: %v", err)
	}
	rr.markDeployed("app-1", "0000000000000000000000000000000000000000")
	if err := rr.RenameApp(context.Background(), "app-1", "again"); err != nil {
		t.Fatal(err)
	}
	if !rr.hasDraft("app-1") {
		t.Fatal("the rename deployed a draft")
	}
}

// A helper that could not even start (the download can't be opened, docker
// can't be run) changed nothing: the app is started again.
func TestRestoreHelperNeverStarted(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	rr.runHelper = func(ctx context.Context, name string, args []string, out io.Writer) (int, []byte, error) {
		_ = os.RemoveAll(rr.restoreTmpDir()) // the pre-restore copy ran; the download is gone
		return rr.helper.run(ctx, name, args, out)
	}
	_, err := rr.restore(context.Background(), "database", id)
	if err == nil || !strings.Contains(err.Error(), "the restore could not start, the app's data is unchanged") ||
		!slices.Equal(rr.compose, []string{"stop", "start"}) || len(rr.restorer.calls) != 0 {
		t.Fatalf("err = %v, compose %v", err, rr.compose)
	}

	rr = newRestoreRepo(t)
	id = rr.point(t, "database")
	rr.runRestoreHelper = func(context.Context, string, []string, io.Reader) (int, []byte, error) {
		return -1, nil, fmt.Errorf("%w: fork/exec: out of memory", errHelperNotStarted)
	}
	_, err = rr.restore(context.Background(), "database", id)
	if err == nil || !strings.Contains(err.Error(), "the restore could not start, the app's data is unchanged: the helper did not start: fork/exec") ||
		!slices.Equal(rr.compose, []string{"stop", "start"}) {
		t.Fatalf("err = %v, compose %v", err, rr.compose)
	}
}

func TestRunDockerIOWithoutDocker(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	exit, _, err := runDockerIO(context.Background(), "wf-restore-x", []string{"run"}, nil, io.Discard)
	if !errors.Is(err, errHelperNotStarted) || exit != -1 {
		t.Fatalf("exit %d, err %v", exit, err)
	}
}

// setRawSize rewrites a point's manifest with another raw size.
func (rr *restoreRepo) setRawSize(t *testing.T, id string, raw int64) {
	t.Helper()
	mpath := filepath.Join(rr.disk, "app-1", "media", id+".json")
	body, err := os.ReadFile(mpath)
	if err != nil {
		t.Fatal(err)
	}
	var m layout.Manifest
	_ = json.Unmarshal(body, &m)
	m.RawSize = raw
	body, _ = json.Marshal(m)
	if err := os.WriteFile(mpath, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A restore whose copy takes longer to put back than the time left is
// refused before the app is stopped; a small one is not.
func TestRestoreBudgetFollowsTheCopysSize(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.filesPoint(t)
	rr.setRawSize(t, id, 100<<30) // 100 GiB: hours at the assumed rate
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	_, err := rr.restore(ctx, "media", id)
	if err == nil || !strings.Contains(err.Error(), "too little time is left to restore safely") || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("err = %v", err)
	}
	if len(rr.compose) != 0 || len(rr.restorer.calls) != 0 {
		t.Fatalf("compose %v, helper %d", rr.compose, len(rr.restorer.calls))
	}
	rr.setRawSize(t, id, 5<<20)
	if _, err := rr.restore(ctx, "media", id); err != nil {
		t.Fatalf("a small copy: %v", err)
	}
}

// The time is looked at again after the pre-restore copy, which may have used
// it: nothing was replaced then, so the app is started again and the
// pre-restore copy kept.
func TestRestoreBudgetIsCheckedAgainAfterThePreRestoreCopy(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.filesPoint(t)
	inner := rr.runHelper
	rr.runHelper = func(ctx context.Context, name string, args []string, out io.Writer) (int, []byte, error) {
		time.Sleep(200 * time.Millisecond) // the pre-restore dump takes its time
		return inner(ctx, name, args, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), restoreMinBudget+100*time.Millisecond)
	defer cancel()
	_, err := rr.restore(ctx, "media", id)
	if err == nil || !strings.Contains(err.Error(), "too little time is left after the pre-restore copy") || !strings.Contains(err.Error(), "pre-restore copy 2026") {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(rr.compose, []string{"stop", "start"}) || len(rr.restorer.calls) != 0 {
		t.Fatalf("compose %v, helper %d", rr.compose, len(rr.restorer.calls))
	}
}

// A files helper stopped from outside before it printed its swapping line has
// only written temp folders: the app is started again. After the line the
// data may be partly replaced, and the app is left stopped.
func TestRestoreStoppedBeforeTheSwapStartsTheAppAgain(t *testing.T) {
	for name, tc := range map[string]struct {
		stderr  string
		compose []string
		want    string
	}{
		"before":  {"progress\n", []string{"stop", "start"}, "before it replaced any data, so the app's data is unchanged"},
		"during":  {helper.SwappingLine + "\n", []string{"stop", "down"}, "the paths may be partly replaced. The app was left stopped"},
		"no line": {"", []string{"stop", "start"}, "before it replaced any data"},
	} {
		t.Run(name, func(t *testing.T) {
			rr := newRestoreRepo(t)
			id := rr.filesPoint(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rr.runRestoreHelper = func(_ context.Context, _ string, _ []string, in io.Reader) (int, []byte, error) {
				_, _ = io.Copy(io.Discard, in)
				cancel() // the agent stops, or the time limit passes
				return -1, []byte(tc.stderr), fmt.Errorf("stopped after its time limit: %w", ctx.Err())
			}
			_, err := rr.restore(ctx, "media", id)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
			if !slices.Equal(rr.compose, tc.compose) {
				t.Fatalf("compose calls = %v, want %v", rr.compose, tc.compose)
			}
		})
	}
}

// setEntries rewrites a point's manifest with other entries.
func (rr *restoreRepo) setEntries(t *testing.T, backup, id string, entries []string) []string {
	t.Helper()
	mpath := filepath.Join(rr.disk, "app-1", backup, id+".json")
	body, err := os.ReadFile(mpath)
	if err != nil {
		t.Fatal(err)
	}
	var m layout.Manifest
	_ = json.Unmarshal(body, &m)
	was := m.Entries
	m.Entries = entries
	body, _ = json.Marshal(m)
	if err := os.WriteFile(mpath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return was
}

// A copy made under a location the backup no longer has is refused before
// anything is touched, instead of being extracted somewhere else; older
// manifests (no entries) are restored as before.
func TestRestoreRefusesACopyOfAnotherLocation(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.filesPoint(t)
	if was := rr.setEntries(t, "media", id, []string{"app/media"}); !slices.Equal(was, []string{"app/media"}) {
		t.Fatalf("the manifest recorded entries %q", was)
	}
	rr.setEntries(t, "media", id, []string{"app/photos"})
	_, err := rr.restore(context.Background(), "media", id)
	if err == nil || !strings.Contains(err.Error(), "the backup's location changed since this copy was made (the copy holds /app/photos; the backup now covers /app/media)") {
		t.Fatalf("err = %v", err)
	}
	if len(rr.compose) != 0 || len(rr.restorer.calls) != 0 {
		t.Fatalf("compose %v, helper %d", rr.compose, len(rr.restorer.calls))
	}
	// A copy of a whole volume cannot be put into a path of it either.
	rr.setEntries(t, "media", id, []string{""})
	if _, err := rr.restore(context.Background(), "media", id); err == nil || !strings.Contains(err.Error(), "the copy holds the whole volume") {
		t.Fatalf("whole volume: %v", err)
	}
	rr.setEntries(t, "media", id, nil) // an older manifest
	if _, err := rr.restore(context.Background(), "media", id); err != nil {
		t.Fatalf("old manifest: %v", err)
	}
}

// A restore point can't be deleted (from another browser, say) while a
// restore of the app runs: the one it names in its error is among them.
func TestDeleteRestorePointIsRefusedDuringARestore(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	var during error
	rr.restorer.onRun = func() {
		during = rr.DeleteRestorePoint(context.Background(), "app-1", "database", id)
	}
	if _, err := rr.restore(context.Background(), "database", id); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(during, ErrRestoring) {
		t.Fatalf("delete during a restore: %v", during)
	}
	if err := rr.DeleteRestorePoint(context.Background(), "app-1", "database", id); err != nil {
		t.Fatalf("delete after the restore: %v", err)
	}
}
