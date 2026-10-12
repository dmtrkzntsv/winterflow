package dockercompose

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/helper"
)

// TestRestoreThroughDocker restores through real helper containers: a
// sqlite database on a named volume (owned by an app user), and files on a
// bind mount with a named volume nested inside it. Each restore puts the
// backed-up data back, keeps a pre-restore copy, and stops and starts the
// app around it.
func TestRestoreThroughDocker(t *testing.T) {
	requireDocker(t)
	r := newTestRepo(t)
	agent := buildAgent(t)
	r.helperBinary = func() (string, error) { return agent, nil }
	var compose []string
	r.runningProjects = func(context.Context) (map[string]bool, error) { return map[string]bool{"wf-itg": true}, nil }
	r.runCompose = func(_ context.Context, _ string, args []string) error {
		compose = append(compose, args[len(args)-1])
		return nil
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	vol, thumbs := "wf-itg-restore-"+stamp, "wf-itg-thumbs-"+stamp
	for _, v := range []string{vol, thumbs} {
		dockerT(t, "volume", "create", v)
		t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", v).Run() })
	}

	// The database: 3 rows, copied into the volume as uid 1000.
	src := t.TempDir()
	makeDB := func(name string, rows int) {
		db, err := sql.Open("sqlite", "file:"+filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY)"); err != nil {
			t.Fatal(err)
		}
		for range rows {
			if _, err := db.Exec("INSERT INTO items DEFAULT VALUES"); err != nil {
				t.Fatal(err)
			}
		}
	}
	makeDB("app.db", 3)
	makeDB("later.db", 7)
	dockerT(t, "run", "--rm", "-v", vol+":/v", "-v", src+":/h:ro", helper.Image, "sh", "-c", "cp /h/app.db /v/app.db && chown -R 1000:1000 /v")

	// The files: a bind mount with the thumbs volume mounted inside it.
	media := t.TempDir()
	writeFileT(t, media, "a.txt", "alpha")
	writeFileT(t, media, "sub/b.txt", "beta")
	if err := os.Mkdir(filepath.Join(media, "thumbs"), 0o755); err != nil {
		t.Fatal(err)
	}
	dockerT(t, "run", "--rm", "-v", thumbs+":/t", helper.Image, "sh", "-c", "echo small > /t/t1.jpg && mkdir /t/2026 && echo deep > /t/2026/t2.jpg")

	disk := t.TempDir()
	if err := r.storages.Save(localStorage("disk", disk), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := saveConfig(t, r, "itg", fmt.Sprintf(`services:
  app:
    image: %s
    volumes:
      - data:/data
      - %s:/media
      - thumbs:/media/thumbs
volumes:
  data:
    name: %s
    external: true
  thumbs:
    name: %s
    external: true
x-winterflow:
  backups:
    - name: database
      type: sqlite
      volume: data
      path: app.db
    - name: media
      type: files
      service: app
      paths: [/media]
`, helper.Image, media, vol, thumbs), withBackups(
		`[{"name":"database","enabled":true,"storage":"disk"},{"name":"media","enabled":true,"storage":"disk"}]`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*restoreMinBudget)
	defer cancel()
	clock := time.Now().UTC().Truncate(time.Second)
	r.now = func() time.Time { clock = clock.Add(time.Second); return clock }

	db, err := r.RunBackup(ctx, BackupJob{AppID: "itg", Backup: "database", Kind: command.PointManual})
	if err != nil {
		t.Fatal(err)
	}
	files, err := r.RunBackup(ctx, BackupJob{AppID: "itg", Backup: "media", Kind: command.PointManual})
	if err != nil {
		t.Fatal(err)
	}

	// The app writes on: a bigger database, edited and new files.
	dockerT(t, "run", "--rm", "-v", vol+":/v", "-v", src+":/h:ro", helper.Image, "sh", "-c",
		"cp /h/later.db /v/app.db && echo stale > /v/app.db-wal && chown -R 1000:1000 /v")
	writeFileT(t, media, "a.txt", "edited")
	writeFileT(t, media, "new.txt", "new")
	dockerT(t, "run", "--rm", "-v", thumbs+":/t", helper.Image, "sh", "-c", "echo changed > /t/t1.jpg && rm -r /t/2026")

	resp, err := r.RestoreBackup(ctx, RestoreJob{AppID: "itg", Backup: "database", Point: db.Point.ID})
	if err != nil {
		t.Fatal(err)
	}
	if resp.PreRestorePoint == "" || !strings.HasSuffix(resp.PreRestorePoint, "-pre-restore") {
		t.Fatalf("response = %+v", resp)
	}
	if _, err := os.Stat(filepath.Join(disk, "itg", "database", resp.PreRestorePoint+".json")); err != nil {
		t.Fatalf("no pre-restore copy: %v", err)
	}
	// The volume holds the backed-up database, owned as before, no -wal.
	out := dockerT(t, "run", "--rm", "-v", vol+":/v", helper.Image, "sh", "-c", "ls -A /v; stat -c %u:%g /v/app.db")
	if out != "app.db\n1000:1000" {
		t.Fatalf("volume after restore: %q", out)
	}
	check := filepath.Join(t.TempDir(), "check")
	if err := os.Mkdir(check, 0o777); err != nil {
		t.Fatal(err)
	}
	dockerT(t, "run", "--rm", "-v", vol+":/v", "-v", check+":/c", helper.Image, "sh", "-c", "cp /v/app.db /c/app.db && chmod 666 /c/app.db")
	cdb, err := sql.Open("sqlite", "file:"+filepath.Join(check, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := cdb.QueryRow("SELECT count(*) FROM items").Scan(&n); err != nil || n != 3 {
		t.Fatalf("restored rows = %d, %v", n, err)
	}
	cdb.Close()

	resp, err = r.RestoreBackup(ctx, RestoreJob{AppID: "itg", Backup: "media", Point: files.Point.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(resp.PreRestorePoint, "-pre-restore") {
		t.Fatalf("response = %+v", resp)
	}
	for rel, want := range map[string]string{"a.txt": "alpha", "sub/b.txt": "beta"} {
		if body, err := os.ReadFile(filepath.Join(media, rel)); err != nil || string(body) != want {
			t.Fatalf("%s = %q, %v", rel, body, err)
		}
	}
	if _, err := os.Stat(filepath.Join(media, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("a file made after the copy survived the restore")
	}
	if info, err := os.Stat(filepath.Join(media, "thumbs")); err != nil || !info.IsDir() {
		t.Fatalf("the nested mount point is gone: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(media, ".wf-restore-*")); len(left) != 0 {
		t.Fatalf("temp folders left: %v", left)
	}
	if out := dockerT(t, "run", "--rm", "-v", thumbs+":/t", helper.Image, "sh", "-c", "cat /t/t1.jpg /t/2026/t2.jpg; ls -A /t"); out != "small\ndeep\n2026\nt1.jpg" {
		t.Fatalf("thumbs after restore: %q", out)
	}
	if !slices.Equal(compose, []string{"stop", "start", "stop", "start"}) {
		t.Fatalf("compose calls = %v", compose)
	}
	if left, _ := os.ReadDir(r.restoreTmpDir()); len(left) != 0 {
		t.Fatalf("downloads left: %v", left)
	}
}

// TestRestoreFilesOwnersAndExcludesThroughDocker restores a bind mount with a
// volume nested inside it, both roots and their files owned by app users the
// agent is not, and a backup that excludes some files. The restore puts the
// owners and modes of both roots and of the files back, and keeps the
// excluded files (the copy never held them) where they were.
func TestRestoreFilesOwnersAndExcludesThroughDocker(t *testing.T) {
	requireDocker(t)
	r := newTestRepo(t)
	agent := buildAgent(t)
	r.helperBinary = func() (string, error) { return agent, nil }
	var compose []string
	r.runningProjects = func(context.Context) (map[string]bool, error) { return map[string]bool{"wf-own": true}, nil }
	r.runCompose = func(_ context.Context, _ string, args []string) error {
		compose = append(compose, args[len(args)-1])
		return nil
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	thumbs := "wf-itg-own-thumbs-" + stamp
	dockerT(t, "volume", "create", thumbs)
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", thumbs).Run() })

	// The bind mount ends up owned by uid 2000: give it back to the test
	// user before the temp dir is removed.
	media := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("docker", "run", "--rm", "-v", media+":/m", helper.Image, "sh", "-c",
			fmt.Sprintf("chown -R %d:%d /m && chmod -R u+rwx /m", os.Getuid(), os.Getgid())).Run()
	})
	// docker is the one that can chown: root in a helper does it.
	dockerT(t, "run", "--rm", "-v", media+":/m", "-v", thumbs+":/t", helper.Image, "sh", "-c", strings.Join([]string{
		"mkdir -p /m/sub /m/thumbs",
		"echo alpha > /m/a.txt", "echo beta > /m/sub/b.txt",
		"echo tmp > /m/skip.tmp", "echo keep > /m/sub/keep.tmp",
		"chown -R 2000:2000 /m", "chmod 750 /m /m/sub", "chmod 640 /m/a.txt",
		"echo small > /t/t1.jpg", "echo cached > /t/cache.tmp",
		"chown -R 3000:3000 /t", "chmod 700 /t", "chmod 600 /t/t1.jpg",
	}, " && "))

	disk := t.TempDir()
	if err := r.storages.Save(localStorage("disk", disk), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := saveConfig(t, r, "own", fmt.Sprintf(`services:
  app:
    image: %s
    volumes:
      - %s:/media
      - thumbs:/media/thumbs
volumes:
  thumbs:
    name: %s
    external: true
x-winterflow:
  backups:
    - name: media
      type: files
      service: app
      paths: [/media]
      exclude: ["*.tmp"]
`, helper.Image, media, thumbs), withBackups(`[{"name":"media","enabled":true,"storage":"disk"}]`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*restoreMinBudget)
	defer cancel()
	clock := time.Now().UTC().Truncate(time.Second)
	r.now = func() time.Time { clock = clock.Add(time.Second); return clock }

	files, err := r.RunBackup(ctx, BackupJob{AppID: "own", Backup: "media", Kind: command.PointManual})
	if err != nil {
		t.Fatal(err)
	}

	// The app writes on, and everything changes hands: new owners and modes
	// on both roots and files, edited and new files, edited excluded files.
	dockerT(t, "run", "--rm", "-v", media+":/m", "-v", thumbs+":/t", helper.Image, "sh", "-c", strings.Join([]string{
		"echo edited > /m/a.txt", "echo new > /m/new.txt", "rm /m/sub/b.txt",
		"echo tmp-later > /m/skip.tmp", "echo keep-later > /m/sub/keep.tmp",
		"chown -R 0:0 /m", "chmod 777 /m /m/sub", "chmod 666 /m/a.txt",
		"echo changed > /t/t1.jpg", "echo cached-later > /t/cache.tmp",
		"chown -R 0:0 /t", "chmod 777 /t",
	}, " && "))

	resp, err := r.RestoreBackup(ctx, RestoreJob{AppID: "own", Backup: "media", Point: files.Point.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(resp.PreRestorePoint, "-pre-restore") {
		t.Fatalf("response = %+v", resp)
	}

	// The bind mount: backed-up files and metadata, the excluded files as
	// the app left them, nothing made since the copy, the mount point there.
	got := dockerT(t, "run", "--rm", "-v", media+":/m", helper.Image, "sh", "-c",
		`cd /m && stat -c '%n %u:%g %a' . a.txt sub sub/b.txt && cat a.txt sub/b.txt skip.tmp sub/keep.tmp && ls -A . sub && test -d thumbs && echo mountpoint`)
	want := strings.Join([]string{
		". 2000:2000 750", "a.txt 2000:2000 640", "sub 2000:2000 750", "sub/b.txt 2000:2000 644",
		"alpha", "beta", "tmp-later", "keep-later",
		".:", "a.txt", "skip.tmp", "sub", "thumbs", "", "sub:", "b.txt", "keep.tmp", "mountpoint",
	}, "\n")
	// busybox ls prints no headers for one operand per call; two operands do.
	if got != want {
		t.Fatalf("media after restore:\n%q\nwant\n%q", got, want)
	}
	// The nested volume: its own root's owner and mode, its files', and the
	// excluded file it had.
	got = dockerT(t, "run", "--rm", "-v", thumbs+":/t", helper.Image, "sh", "-c",
		`cd /t && stat -c '%n %u:%g %a' . t1.jpg && cat t1.jpg cache.tmp && ls -A .`)
	want = strings.Join([]string{". 3000:3000 700", "t1.jpg 3000:3000 600", "small", "cached-later", "cache.tmp", "t1.jpg"}, "\n")
	if got != want {
		t.Fatalf("thumbs after restore:\n%q\nwant\n%q", got, want)
	}
	if !slices.Equal(compose, []string{"stop", "start"}) {
		t.Fatalf("compose calls = %v", compose)
	}
	if left, _ := os.ReadDir(r.restoreTmpDir()); len(left) != 0 {
		t.Fatalf("downloads left: %v", left)
	}
}
