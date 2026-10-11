package dockercompose

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/helper"
)

// requireDocker skips a test without a usable Docker daemon (and in -short).
func requireDocker(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("docker test in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("docker is not available:", err)
	}
}

// buildAgent builds the agent binary the helper runs: static, so it runs
// inside alpine.
func buildAgent(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "winterflow-agent")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", out, "winterflow/cmd/agent")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the agent: %v\n%s", err, msg)
	}
	return out
}

func dockerT(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestBackupThroughDocker runs real helper containers: a sqlite database on
// a named volume (owned by an app user, WAL mode) and files on a bind
// mount, then an unchanged run, a run killed at its deadline, and the
// startup cleanup of leftover helpers.
func TestBackupThroughDocker(t *testing.T) {
	requireDocker(t)
	r := newTestRepo(t)
	agent := buildAgent(t)
	r.helperBinary = func() (string, error) { return agent, nil }
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	vol := "wf-itg-backup-" + stamp
	dockerT(t, "volume", "create", vol)
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", vol).Run() })

	// The database: made here, copied into the volume as uid 1000.
	src := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(src, "app.db")+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, body TEXT); INSERT INTO items (body) VALUES ('a'), ('b'), ('c')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	dockerT(t, "run", "--rm", "-v", vol+":/v", "-v", src+":/h:ro", helper.Image, "sh", "-c", "cp /h/app.db /v/app.db && chown -R 1000:1000 /v")

	// The files: a bind mount.
	media := t.TempDir()
	writeFileT(t, media, "a.txt", "alpha")
	writeFileT(t, media, "sub/b.txt", "beta")
	writeFileT(t, media, "skip.tmp", "no")

	compose := fmt.Sprintf(`services:
  app:
    image: %s
    volumes:
      - data:/data
      - %s:/media
volumes:
  data:
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
      exclude: ["*.tmp"]
`, helper.Image, media, vol)
	disk := t.TempDir()
	if err := r.storages.Save(localStorage("disk", disk), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := saveConfig(t, r, "itg", compose, withBackups(
		`[{"name":"database","enabled":true,"storage":"disk"},{"name":"media","enabled":true,"storage":"disk"}]`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// A scheduled first copy: with a retention ceiling set, an unchanged run
	// compares only with a complete scheduled copy (a manual one alone makes
	// the next run upload a scheduled one, see layout.NeedsRefresh).
	res, err := r.RunBackup(ctx, BackupJob{AppID: "itg", Backup: "database", Kind: command.PointScheduled})
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "copy.db")
	if err := os.WriteFile(copyPath, decompress(t, filepath.Join(disk, "itg", "database", res.Point.ID+".db.zst")), 0o600); err != nil {
		t.Fatal(err)
	}
	cp, err := sql.Open("sqlite", "file:"+copyPath)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := cp.QueryRow("SELECT count(*) FROM items").Scan(&n); err != nil || n != 3 {
		t.Fatalf("copy rows = %d, %v", n, err)
	}
	cp.Close()
	// The helper opened the database as its owner: no root-owned -shm.
	for _, uid := range strings.Fields(dockerT(t, "run", "--rm", "-v", vol+":/v", helper.Image, "sh", "-c", "stat -c %u /v/*")) {
		if uid != "1000" {
			t.Fatalf("a file in the volume is owned by %s", uid)
		}
	}

	// Unchanged since the copy: exit 3, nothing uploaded.
	again, err := r.RunBackup(ctx, BackupJob{AppID: "itg", Backup: "database", Kind: command.PointScheduled})
	if err != nil || !again.Unchanged {
		t.Fatalf("unchanged run = %+v, %v", again, err)
	}

	res, err = r.RunBackup(ctx, BackupJob{AppID: "itg", Backup: "media", Kind: command.PointManual})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(decompress(t, filepath.Join(disk, "itg", "media", res.Point.ID+".tar.zst"))))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		names[h.Name] = string(body)
	}
	if names["media/a.txt"] != "alpha" || names["media/sub/b.txt"] != "beta" || len(names) != 4 {
		t.Fatalf("archive = %v", names)
	}

	// A run past its deadline: the helper container is removed.
	name := "wf-backup-itg-" + stamp
	kctx, kcancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer kcancel()
	_, _, err = runDockerHelper(kctx, name, []string{"run", "--rm", "--name", name, "--label", helper.RoleLabel, helper.Image, "sleep", "60"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "time limit") {
		t.Fatalf("killed run: %v", err)
	}
	if left := dockerT(t, "ps", "-aq", "--filter", "name="+name); left != "" {
		t.Fatalf("the killed helper is still there: %s", left)
	}

	// Leftover helpers from a crashed agent are removed at start.
	dockerT(t, "run", "-d", "--label", helper.RoleLabel, "--label", helper.AppLabel+"=itg", helper.Image, "sleep", "60")
	if err := r.RemoveHelpers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if left := dockerT(t, "ps", "-aq", "--filter", "label="+helper.RoleLabel); left != "" {
		t.Fatalf("leftover helpers: %s", left)
	}
}

func decompress(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := zstd.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	out, err := io.ReadAll(dec)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
