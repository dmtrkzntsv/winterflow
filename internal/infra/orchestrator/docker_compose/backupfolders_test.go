package dockercompose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
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
	marker := `{"deleted_at":"2026-01-01T00:00:00Z","app_name":"Shop"}`
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
	putObject(t, b.disk, "gone-1/.deleted", `{"deleted_at":"2026-10-01T00:00:00Z","app_name":"Shop"}`, now)
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
		{AppID: "gone-1", AppName: "Shop", DeletedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix(),
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
