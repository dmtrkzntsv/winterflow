package dockercompose

import (
	"context"
	"encoding/json"
	"errors"
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
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/backup/store"
)

// putObject writes a storage object at disk/key with mtime.
func putObject(t *testing.T, disk, key, body string, mtime time.Time) {
	t.Helper()
	writeFileT(t, disk, key, body)
	if err := os.Chtimes(filepath.Join(disk, key), mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// putPoint writes a complete point (data object and manifest) made at at.
func putPoint(t *testing.T, disk, app, backup string, at time.Time, kind command.PointKind) {
	t.Helper()
	id := at.UTC().Format("20060102T150405Z") + "-" + string(kind)
	putObject(t, disk, app+"/"+backup+"/"+id+".db.zst", "data", at)
	putObject(t, disk, app+"/"+backup+"/"+id+".json", "{}", at)
}

// Deleting an app keeps its backups, marked with the app's name and when it
// was deleted; delete_backups removes them.
func TestDeleteAppSettlesBackups(t *testing.T) {
	b := newBackupRepo(t)
	b.runCompose = func(context.Context, string, []string) error { return nil }
	if _, err := b.run(t, "database", command.PointScheduled); err != nil {
		t.Fatal(err)
	}
	res, err := b.DeleteApp(context.Background(), "app-1", false, false)
	if err != nil || res.BackupsDeleted || len(res.Warnings) != 0 {
		t.Fatalf("delete: %+v, %v", res, err)
	}
	raw, err := os.ReadFile(filepath.Join(b.disk, "app-1", DeletedMarkerName))
	if err != nil {
		t.Fatal(err)
	}
	var m DeletedMarker
	if err := json.Unmarshal(raw, &m); err != nil || m.AppName != "demo" || !m.DeletedAt.Equal(b.clock) {
		t.Fatalf("marker = %s (%v)", raw, err)
	}
	if got := b.stored(t); len(got) != 3 {
		t.Fatalf("stored = %v", got)
	}

	b = newBackupRepo(t)
	b.runCompose = func(context.Context, string, []string) error { return nil }
	if _, err := b.run(t, "database", command.PointScheduled); err != nil {
		t.Fatal(err)
	}
	res, err = b.DeleteApp(context.Background(), "app-1", false, true)
	if err != nil || !res.BackupsDeleted || len(res.Warnings) != 0 {
		t.Fatalf("delete with backups: %+v, %v", res, err)
	}
	if got := b.stored(t); len(got) != 0 {
		t.Fatalf("stored = %v", got)
	}
	// An app this agent doesn't have deletes no backups.
	if res, err := b.DeleteApp(context.Background(), "nope", false, true); err != nil || res.BackupsDeleted {
		t.Fatalf("unknown app: %+v, %v", res, err)
	}
}

// A storage that fails is a warning: the app is deleted anyway.
func TestDeleteAppBackupWarnings(t *testing.T) {
	b := newBackupRepo(t)
	b.runCompose = func(context.Context, string, []string) error { return nil }
	if _, err := b.storages.Delete("disk"); err != nil {
		t.Fatal(err)
	}
	res, err := b.DeleteApp(context.Background(), "app-1", false, false)
	if err != nil || len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "backups in disk: mark the backups of the deleted app: there is no storage disk") {
		t.Fatalf("%+v, %v", res, err)
	}
	if b.appExists("app-1") {
		t.Fatal("the app is still there")
	}
}

func TestSweepDeleted(t *testing.T) {
	b := newBackupRepo(t)
	now := b.clock
	day := 24 * time.Hour
	// Deleted after its newest point: older markers are stale (see
	// TestSweepSkipsAStaleMarker).
	marker := `{"deleted_at":"` + now.Add(-5*day).Format(time.RFC3339) + `","app_name":"Shop"}`
	// gone-1: deleted, one copy past the ceiling (180 days), one within.
	putObject(t, b.disk, "gone-1/.deleted", marker, now)
	putPoint(t, b.disk, "gone-1", "db", now.Add(-200*day), command.PointScheduled)
	putPoint(t, b.disk, "gone-1", "db", now.Add(-10*day), command.PointManual)
	// gone-2: deleted, everything old, a custom tool's file too.
	putObject(t, b.disk, "gone-2/.deleted", marker, now)
	putPoint(t, b.disk, "gone-2", "db", now.Add(-300*day), command.PointScheduled)
	putObject(t, b.disk, "gone-2/replica/generations/x.wal", "wal", now.Add(-190*day))
	// other: no marker (another server's): never deleted.
	putPoint(t, b.disk, "other", "db", now.Add(-400*day), command.PointScheduled)
	// app-1 is on this server: a stray marker doesn't make it sweepable.
	putObject(t, b.disk, "app-1/.deleted", marker, now)
	putPoint(t, b.disk, "app-1", "database", now.Add(-400*day), command.PointManual)

	if problems := b.SweepDeleted(context.Background()); len(problems) != 0 {
		t.Fatal(problems)
	}
	got := b.stored(t)
	want := []string{
		"app-1/.deleted", "app-1/database/20250906T120000Z-manual.db.zst", "app-1/database/20250906T120000Z-manual.json",
		"gone-1/.deleted", "gone-1/db/20261001T120000Z-manual.db.zst", "gone-1/db/20261001T120000Z-manual.json",
		"other/db/20250906T120000Z-scheduled.db.zst", "other/db/20250906T120000Z-scheduled.json",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("stored =\n%v\nwant\n%v", got, want)
	}

	// No ceiling: no sweep.
	t.Setenv("BACKUP_RETENTION_DAYS", "0")
	putPoint(t, b.disk, "gone-1", "db", now.Add(-900*day), command.PointScheduled)
	if problems := b.SweepDeleted(context.Background()); problems != nil || len(b.stored(t)) != len(want)+2 {
		t.Fatalf("swept with no ceiling: %v %v", problems, b.stored(t))
	}
	// A storage that can't be opened is reported.
	t.Setenv("BACKUP_RETENTION_DAYS", "30")
	if err := os.RemoveAll(b.disk); err != nil {
		t.Fatal(err)
	}
	if problems := b.SweepDeleted(context.Background()); len(problems) != 1 || !strings.HasPrefix(problems[0], "disk: ") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestOrphans(t *testing.T) {
	b := newBackupRepo(t)
	now := b.clock
	day := 24 * time.Hour
	putObject(t, b.disk, "gone-1/.deleted", `{"deleted_at":"2026-10-08T00:00:00Z","app_name":"Shop"}`, now)
	putPoint(t, b.disk, "gone-1", "db", now.Add(-20*day), command.PointScheduled)
	putPoint(t, b.disk, "gone-1", "media", now.Add(-5*day), command.PointManual)
	putObject(t, b.disk, "gone-1/db/20261011T110000Z-scheduled.db.zst", "incomplete", now) // no manifest
	putPoint(t, b.disk, "other", "db", now.Add(-2*day), command.PointScheduled)
	putObject(t, b.disk, "empty/.deleted", `{"deleted_at":"2026-10-02T00:00:00Z","app_name":"Gone"}`, now)
	putObject(t, b.disk, "uploads/photo.jpg", "an app's own files", now)
	putObject(t, b.disk, "top-level.txt", "x", now)
	putPoint(t, b.disk, "app-1", "database", now.Add(-day), command.PointManual)

	got, err := b.ListOrphans(context.Background(), "disk")
	if err != nil {
		t.Fatal(err)
	}
	want := []command.Orphan{
		{AppID: "empty", AppName: "Gone", DeletedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC).Unix(),
			PurgeAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC).Unix()},
		{AppID: "gone-1", AppName: "Shop", DeletedAt: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC).Unix(),
			PurgeAt: now.Add(-time.Hour + 180*day).Unix(), Copies: 2, Bytes: 4*2 + 2*2 + 10, Newest: now.Add(-5 * day).Unix()},
		{AppID: "other", Copies: 1, Bytes: 6, Newest: now.Add(-2 * day).Unix()},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("orphans =\n%+v\nwant\n%+v", got, want)
	}

	if _, err := b.DeleteOrphan(context.Background(), "disk", "app-1"); err == nil || !strings.Contains(err.Error(), "the app demo is on this server") {
		t.Fatalf("own app: %v", err)
	}
	n, err := b.DeleteOrphan(context.Background(), "disk", "gone-1")
	if err != nil || n != 6 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(b.disk, "gone-1")); !os.IsNotExist(err) {
		t.Fatal("the orphan's folder is still there")
	}
	if _, err := b.ListOrphans(context.Background(), "nope"); err == nil || !strings.Contains(err.Error(), "there is no storage nope") {
		t.Fatalf("unknown storage: %v", err)
	}
}

// app.get reports where a new backup can point and the server's ceiling.
func TestGetAppReportsBackupTargets(t *testing.T) {
	b := newBackupRepo(t)
	t.Setenv("BACKUP_RETENTION_DAYS", "90")
	resp, err := b.GetApp(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if resp.RetentionDays != 90 || resp.BackupTargets == nil || !slices.Equal(resp.BackupTargets.Volumes, []string{"data"}) ||
		len(resp.BackupTargets.Services) != 2 || resp.BackupTargets.Services[1].Name != "web" ||
		len(resp.BackupTargets.Services[1].Mounts) != 2 {
		t.Fatalf("response = %+v %+v", resp.RetentionDays, resp.BackupTargets)
	}
}

// failingStore fails chosen calls and records the keys deleted, in order.
type failingStore struct {
	store.Store
	failDelete func(keys []string) error
	failPut    func(key string) error
	failList   error
	mu         *sync.Mutex
	deleted    *[]string
}

func (f failingStore) Delete(ctx context.Context, keys ...string) error {
	if f.failDelete != nil {
		if err := f.failDelete(keys); err != nil {
			return err
		}
	}
	if err := f.Store.Delete(ctx, keys...); err != nil {
		return err
	}
	f.mu.Lock()
	*f.deleted = append(*f.deleted, keys...)
	f.mu.Unlock()
	return nil
}

func (f failingStore) Put(ctx context.Context, key string, r io.Reader) error {
	if f.failPut != nil {
		if err := f.failPut(key); err != nil {
			return err
		}
	}
	return f.Store.Put(ctx, key, r)
}

func (f failingStore) List(ctx context.Context, prefix string) ([]store.Object, error) {
	if f.failList != nil {
		return nil, f.failList
	}
	return f.Store.List(ctx, prefix)
}

// fail opens the storages through f; deleted collects the deleted keys.
func (b *backupRepo) fail(f failingStore) *[]string {
	f.mu, f.deleted = &sync.Mutex{}, &[]string{}
	b.Repository.openStorage = func(s model.Storage, secret string) (store.Store, error) {
		st, err := store.Open(s, secret)
		f.Store = st
		return f, err
	}
	return f.deleted
}

func isMarker(key string) bool { return strings.HasSuffix(key, "/"+DeletedMarkerName) }

func anyData(keys []string) bool {
	for _, k := range keys {
		if !isMarker(k) {
			return true
		}
	}
	return false
}

// A purge marks the folder first and unmarks it last: a delete that fails
// leaves the marker, the response says the backups are not (all) deleted,
// and the sweep finishes the job once the copies pass the ceiling.
func TestDeleteAppPurgeFailureLeavesTheFolderMarked(t *testing.T) {
	b := newBackupRepo(t)
	b.runCompose = func(context.Context, string, []string) error { return nil }
	if _, err := b.run(t, "database", command.PointScheduled); err != nil {
		t.Fatal(err)
	}
	failing := true
	b.fail(failingStore{failDelete: func(keys []string) error {
		if failing && anyData(keys) {
			return errors.New("disk is full of excuses")
		}
		return nil
	}})
	res, err := b.DeleteApp(context.Background(), "app-1", false, true)
	if err != nil || res.BackupsDeleted || len(res.Warnings) != 1 ||
		!strings.Contains(res.Warnings[0], "disk is full of excuses; the folder stays marked as deleted") {
		t.Fatalf("delete: %+v, %v", res, err)
	}
	got := b.stored(t)
	if !slices.Contains(got, "app-1/"+DeletedMarkerName) || len(got) != 3 {
		t.Fatalf("stored = %v", got)
	}
	// The sweep completes it once the copies are past the ceiling.
	failing = false
	b.clock = b.clock.Add(181 * 24 * time.Hour)
	if problems := b.SweepDeleted(context.Background()); len(problems) != 0 {
		t.Fatal(problems)
	}
	if got := b.stored(t); len(got) != 0 {
		t.Fatalf("after the sweep: %v", got)
	}
}

// The marker is the last key a purge deletes; a marker that can't be written
// stops the purge before anything goes.
func TestDeleteAppPurgeOrderAndMarkerFailure(t *testing.T) {
	b := newBackupRepo(t)
	b.runCompose = func(context.Context, string, []string) error { return nil }
	if _, err := b.run(t, "database", command.PointScheduled); err != nil {
		t.Fatal(err)
	}
	deleted := b.fail(failingStore{})
	if res, err := b.DeleteApp(context.Background(), "app-1", false, true); err != nil || !res.BackupsDeleted || len(res.Warnings) != 0 {
		t.Fatalf("delete: %+v, %v", res, err)
	}
	if n := len(*deleted); n != 3 || (*deleted)[n-1] != "app-1/"+DeletedMarkerName {
		t.Fatalf("deleted = %v", *deleted)
	}

	b = newBackupRepo(t)
	b.runCompose = func(context.Context, string, []string) error { return nil }
	if _, err := b.run(t, "database", command.PointScheduled); err != nil {
		t.Fatal(err)
	}
	deleted = b.fail(failingStore{failPut: func(key string) error {
		if isMarker(key) {
			return errors.New("no writes")
		}
		return nil
	}})
	res, err := b.DeleteApp(context.Background(), "app-1", false, true)
	if err != nil || res.BackupsDeleted || len(res.Warnings) != 1 ||
		!strings.Contains(res.Warnings[0], "write the marker: no writes; the folder may still hold backups") {
		t.Fatalf("delete: %+v, %v", res, err)
	}
	if len(*deleted) != 0 || len(b.stored(t)) != 2 {
		t.Fatalf("deleted %v, stored %v", *deleted, b.stored(t))
	}
}

// An app whose backup settings can't be read is deleted; its folders are
// left as they are, with a warning.
func TestDeleteAppUnreadableBackupSettings(t *testing.T) {
	b := newBackupRepo(t)
	b.runCompose = func(context.Context, string, []string) error { return nil }
	writeFileT(t, b.appDataDir("app-1"), "compose.yml", strings.Replace(backupCompose, "volume: data", "volume: gone", 1))
	res, err := b.DeleteApp(context.Background(), "app-1", false, true)
	if err != nil || res.BackupsDeleted || len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "backups: the app's backup settings can't be read") {
		t.Fatalf("delete: %+v, %v", res, err)
	}
}

// Deleting an orphan marks it first (when it isn't) and unmarks it last, so a
// failure leaves a marked folder; only a folder holding backups is deleted.
func TestDeleteOrphanFailureLeavesAMarker(t *testing.T) {
	b := newBackupRepo(t)
	now := b.clock
	putPoint(t, b.disk, "other", "db", now.Add(-2*24*time.Hour), command.PointScheduled)
	failing := true
	b.fail(failingStore{failDelete: func(keys []string) error {
		if failing && anyData(keys) {
			return errors.New("denied")
		}
		return nil
	}})
	if _, err := b.DeleteOrphan(context.Background(), "disk", "other"); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("delete: %v", err)
	}
	if got := b.stored(t); !slices.Contains(got, "other/.deleted") || len(got) != 3 {
		t.Fatalf("stored = %v", got)
	}
	orphans, err := b.ListOrphans(context.Background(), "disk")
	if err != nil || len(orphans) != 1 || orphans[0].DeletedAt != now.Unix() || orphans[0].PurgeAt == 0 {
		t.Fatalf("orphans = %+v, %v", orphans, err)
	}
	failing = false
	b.clock = now.Add(181 * 24 * time.Hour)
	if problems := b.SweepDeleted(context.Background()); len(problems) != 0 || len(b.stored(t)) != 0 {
		t.Fatalf("sweep: %v, stored %v", problems, b.stored(t))
	}
}

func TestDeleteOrphanChecks(t *testing.T) {
	b := newBackupRepo(t)
	ctx := context.Background()
	putObject(t, b.disk, "uploads/photo.jpg", "an app's own files", b.clock)
	if _, err := b.DeleteOrphan(ctx, "disk", "uploads"); err == nil || !strings.Contains(err.Error(), "holds no backups") {
		t.Fatalf("a folder without backups: %v", err)
	}
	if got := b.stored(t); len(got) != 1 {
		t.Fatalf("stored = %v", got)
	}
	if _, err := b.DeleteOrphan(ctx, "disk", "app-1/database"); err == nil {
		t.Fatal("a path was taken for an app id")
	}
	if n, err := b.DeleteOrphan(ctx, "disk", "ghost"); err != nil || n != 0 {
		t.Fatalf("nothing there: %d, %v", n, err)
	}
	if _, err := b.DeleteOrphan(ctx, "nope", "ghost"); err == nil || !strings.Contains(err.Error(), "there is no storage nope") {
		t.Fatalf("unknown storage: %v", err)
	}
	// A folder longer than one delete batch goes whole.
	putObject(t, b.disk, "big/.deleted", `{"deleted_at":"2026-10-01T00:00:00Z"}`, b.clock)
	for i := 0; i < 2*deleteBatch+50; i++ {
		putObject(t, b.disk, "big/files/"+strconv.Itoa(i)+".wal", "w", b.clock)
	}
	if n, err := b.DeleteOrphan(ctx, "disk", "big"); err != nil || n != 2*deleteBatch+51 {
		t.Fatalf("big: %d, %v", n, err)
	}
	if got := b.stored(t); len(got) != 1 {
		t.Fatalf("stored = %v", got)
	}
}

// A marker older than a restore point is stale (the app was created again
// under its id, maybe on another server): the sweep leaves the folder alone
// and says so, and the orphans list shows it without the marker's data.
func TestSweepSkipsAStaleMarker(t *testing.T) {
	b := newBackupRepo(t)
	now := b.clock
	day := 24 * time.Hour
	putObject(t, b.disk, "live/.deleted", `{"deleted_at":"`+now.Add(-30*day).Format(time.RFC3339)+`","app_name":"Shop"}`, now.Add(-30*day))
	putPoint(t, b.disk, "live", "db", now.Add(-300*day), command.PointScheduled)
	putPoint(t, b.disk, "live", "db", now.Add(-10*day), command.PointManual)
	putObject(t, b.disk, "live/replica/x.wal", "wal", now.Add(-250*day))
	before := b.stored(t)

	problems := b.SweepDeleted(context.Background())
	if len(problems) != 1 || !strings.HasPrefix(problems[0], "disk: app live: a restore point is newer than the folder's deleted marker") {
		t.Fatalf("problems = %v", problems)
	}
	if !slices.Equal(b.stored(t), before) {
		t.Fatalf("stored = %v", b.stored(t))
	}
	orphans, err := b.ListOrphans(context.Background(), "disk")
	if err != nil || len(orphans) != 1 || orphans[0].AppName != "" || orphans[0].DeletedAt != 0 || orphans[0].PurgeAt != 0 || orphans[0].Copies != 2 {
		t.Fatalf("orphans = %+v, %v", orphans, err)
	}
	// An unreadable marker is dated by its mtime.
	putObject(t, b.disk, "live/.deleted", "not json", now.Add(-5*day))
	if problems := b.SweepDeleted(context.Background()); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
}

// One folder that can't be swept doesn't stop the others; a storage that
// can't be listed is reported.
func TestSweepGoesOnPastAFailingFolder(t *testing.T) {
	b := newBackupRepo(t)
	now := b.clock
	day := 24 * time.Hour
	marker := `{"deleted_at":"` + now.Add(-1*day).Format(time.RFC3339) + `"}`
	for _, app := range []string{"gone-1", "gone-2"} {
		putObject(t, b.disk, app+"/.deleted", marker, now)
		putPoint(t, b.disk, app, "db", now.Add(-300*day), command.PointScheduled)
	}
	b.fail(failingStore{failDelete: func(keys []string) error {
		if strings.HasPrefix(keys[0], "gone-1/") {
			return errors.New("denied")
		}
		return nil
	}})
	problems := b.SweepDeleted(context.Background())
	if len(problems) != 1 || !strings.HasPrefix(problems[0], "disk: app gone-1: denied") {
		t.Fatalf("problems = %v", problems)
	}
	got := b.stored(t)
	if len(got) != 3 || !slices.Contains(got, "gone-1/.deleted") {
		t.Fatalf("stored = %v", got) // gone-1 untouched but marked, gone-2 gone
	}
	b.fail(failingStore{failList: errors.New("listing failed")})
	if problems := b.SweepDeleted(context.Background()); len(problems) != 1 || problems[0] != "disk: listing failed" {
		t.Fatalf("problems = %v", problems)
	}
}

// A backup run of a live app removes a stale deleted marker from its folder.
func TestRunBackupClearsAStaleMarker(t *testing.T) {
	b := newBackupRepo(t)
	marker := "app-1/" + DeletedMarkerName
	putObject(t, b.disk, marker, `{"deleted_at":"2026-01-01T00:00:00Z","app_name":"demo"}`, b.clock)
	if _, err := b.run(t, "database", command.PointScheduled); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(b.stored(t), marker) {
		t.Fatalf("stored = %v", b.stored(t))
	}
	// An unchanged run does too.
	putObject(t, b.disk, marker, `{}`, b.clock)
	b.clock = b.clock.Add(time.Hour)
	if res, err := b.run(t, "database", command.PointScheduled); err != nil || !res.Unchanged {
		t.Fatalf("second run: %+v, %v", res, err)
	}
	if slices.Contains(b.stored(t), marker) {
		t.Fatalf("stored = %v", b.stored(t))
	}
}
