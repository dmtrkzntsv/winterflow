package dockercompose

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/backup/helper"
	"winterflow/internal/infra/backup/layout"
	"winterflow/internal/infra/backup/store"
)

// fakeHelper stands in for `docker run` of the helper: it records the
// arguments and answers with a dump of payload whose fingerprint is fp
// (exit 3 when --since already is fp), or with exit/stderr when set.
type fakeHelper struct {
	mu      sync.Mutex
	calls   [][]string
	payload string
	fp      string
	exit    int
	stderr  string
	err     error
	junk    int  // bytes of incompressible output written before a failure
	noReply bool // succeed without the result line
}

func (f *fakeHelper) run(_ context.Context, _ string, args []string, out io.Writer) (int, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, args)
	if f.junk > 0 {
		buf := make([]byte, f.junk)
		rand.New(rand.NewSource(1)).Read(buf)
		if _, err := out.Write(buf); err != nil {
			return 1, nil, err
		}
	}
	if f.noReply {
		_, _ = io.WriteString(out, f.payload)
		return 0, []byte("progress\n"), nil
	}
	if f.err != nil || f.exit != 0 {
		return f.exit, []byte(f.stderr), f.err
	}
	if i := slices.Index(args, "--since"); i >= 0 && args[i+1] == f.fp {
		return helper.ExitUnchanged, []byte(`{"unchanged":true,"warnings":["a note"]}` + "\n"), nil
	}
	if _, err := io.WriteString(out, f.payload); err != nil {
		return 1, nil, err
	}
	res, _ := json.Marshal(helper.Result{Fingerprint: f.fp, RawSize: int64(len(f.payload)), Warnings: []string{"a note"}})
	return 0, append([]byte("progress\n"), res...), nil
}

func (f *fakeHelper) last() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

// backupRepo is an app with backupCompose, its database and media backups
// on the local storage "disk", a fake helper, compose config and docker, and
// a clock at now.
type backupRepo struct {
	*Repository
	disk   string
	helper *fakeHelper
	docker []string
	clock  time.Time
}

func newBackupRepo(t *testing.T) *backupRepo {
	t.Helper()
	r := newTestRepo(t)
	b := &backupRepo{Repository: r, disk: t.TempDir(), helper: &fakeHelper{payload: "SQLITE-SNAPSHOT", fp: "sqlite:1"},
		clock: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	if err := r.storages.Save(localStorage("disk", b.disk), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(
		`[{"name":"database","enabled":true,"storage":"disk"},{"name":"media","enabled":true,"storage":"disk","exclude":["*.tmp"]}]`)); err != nil {
		t.Fatal(err)
	}
	(&fakeComposeConfig{out: configJSON}).install(r)
	r.runHelper = b.helper.run
	r.docker = func(_ context.Context, args ...string) error {
		b.docker = append(b.docker, strings.Join(args, " "))
		return nil
	}
	r.helperBinary = func() (string, error) { return "/usr/local/bin/winterflow", nil }
	r.now = func() time.Time { return b.clock }
	return b
}

func (b *backupRepo) run(t *testing.T, name string, kind command.PointKind) (BackupResult, error) {
	t.Helper()
	return b.RunBackup(context.Background(), BackupJob{AppID: "app-1", Backup: name, Kind: kind})
}

// stored lists the disk's object keys.
func (b *backupRepo) stored(t *testing.T) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(b.disk, func(p string, d os.DirEntry, _ error) error {
		if !d.IsDir() {
			rel, _ := filepath.Rel(b.disk, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

func TestRunBackupStoresAPoint(t *testing.T) {
	b := newBackupRepo(t)
	res, err := b.run(t, "database", command.PointScheduled)
	if err != nil {
		t.Fatal(err)
	}
	id := "20261011T120000Z-scheduled"
	if res.Unchanged || res.Point == nil || res.Point.ID != id || res.Fingerprint != "sqlite:1" ||
		len(res.Warnings) != 1 || res.Warnings[0] != "a note" {
		t.Fatalf("result = %+v", res)
	}
	want := []string{"app-1/database/" + id + ".db.zst", "app-1/database/" + id + ".json"}
	if got := b.stored(t); !slices.Equal(got, want) {
		t.Fatalf("stored = %v", got)
	}
	data, _ := os.ReadFile(filepath.Join(b.disk, want[0]))
	dec, _ := zstd.NewReader(bytes.NewReader(data))
	plain, err := io.ReadAll(dec)
	if err != nil || string(plain) != "SQLITE-SNAPSHOT" {
		t.Fatalf("stored copy = %q, %v", plain, err)
	}
	var m layout.Manifest
	raw, _ := os.ReadFile(filepath.Join(b.disk, want[1]))
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if m.SHA256 != hex.EncodeToString(sum[:]) || m.Size != int64(len(data)) || m.RawSize != 15 || m.Fingerprint != "sqlite:1" ||
		m.Type != recipe.TypeSQLite || m.Kind != command.PointScheduled || m.AppID != "app-1" || m.Backup != "database" || m.AgentVersion == "" {
		t.Fatalf("manifest = %+v", m)
	}
	if res.Size != m.Size || res.Point.Size != m.Size || res.Point.Expires != b.clock.Add(180*24*time.Hour).Unix() {
		t.Fatalf("point = %+v", res.Point)
	}
	args := strings.Join(b.helper.last(), " ")
	for _, part := range []string{
		"run --rm --name wf-backup-",
		"--log-driver none --network none --memory 256m --cpu-shares 256 --cap-drop ALL --cap-add DAC_OVERRIDE --cap-add SETUID --cap-add SETGID --security-opt no-new-privileges",
		"--label winterflow.role=backup-helper --label winterflow.app=app-1",
		"--mount type=volume,source=wf-app-1_data,target=/src/0 --mount type=bind,source=/usr/local/bin/winterflow,target=/wf,readonly",
		"alpine:3.20 /wf backup dump sqlite /src/0/app.db",
	} {
		if !strings.Contains(args, part) {
			t.Errorf("args lack %q:\n%s", part, args)
		}
	}
	if strings.Contains(args, "--since") {
		t.Fatal("the first copy compared with nothing")
	}
	if !slices.Contains(b.docker, "image inspect --format {{.Id}} alpine:3.20") || !slices.Contains(b.docker, "volume inspect --format {{.Name}} wf-app-1_data") {
		t.Fatalf("docker calls = %v", b.docker)
	}

	// Unchanged data: compared with the newest copy, nothing uploaded, and
	// the image is not looked up again.
	b.docker = nil
	b.clock = b.clock.Add(time.Hour)
	res, err = b.run(t, "database", command.PointScheduled)
	if err != nil || !res.Unchanged || res.Point != nil || res.Fingerprint != "sqlite:1" {
		t.Fatalf("unchanged = %+v, %v", res, err)
	}
	if !strings.HasSuffix(strings.Join(b.helper.last(), " "), "--since sqlite:1") || len(b.stored(t)) != 2 {
		t.Fatalf("args = %v, stored = %v", b.helper.last(), b.stored(t))
	}
	if slices.Contains(b.docker, "image inspect --format {{.Id}} alpine:3.20") {
		t.Fatal("the image was looked up twice")
	}
	// A pre-restore copy is always made.
	if res, err := b.run(t, "database", command.PointPreRestore); err != nil || res.Unchanged {
		t.Fatalf("pre-restore = %+v, %v", res, err)
	}
	// Every copy deleted by hand: the next run compares with nothing, so
	// unchanged data is uploaded again.
	if err := os.RemoveAll(filepath.Join(b.disk, "app-1")); err != nil {
		t.Fatal(err)
	}
	if res, err := b.run(t, "database", command.PointScheduled); err != nil || res.Unchanged || slices.Contains(b.helper.last(), "--since") {
		t.Fatalf("after deleting every copy: %+v, %v", res, err)
	}
}

func TestRunBackupFiles(t *testing.T) {
	b := newBackupRepo(t)
	if _, err := b.run(t, "media", command.PointManual); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(b.helper.last(), " ")
	if !strings.Contains(args, "--mount type=bind,source=/data/apps-data/app-1/media,target=/src/0,readonly") ||
		!strings.HasSuffix(args, "/wf backup dump files --src /src/0 --entry app/media --exclude *.tmp") {
		t.Fatalf("args = %s", args)
	}
	if got := b.stored(t); len(got) != 2 || got[1] != "app-1/media/20261011T120000Z-manual.tar.zst" {
		t.Fatalf("stored = %v", got)
	}
}

// When the newest scheduled copy is older than half the ceiling, the run
// uploads even unchanged data, so an idle backup keeps a copy.
func TestRunBackupRefreshesBeforeTheCeiling(t *testing.T) {
	b := newBackupRepo(t)
	t.Setenv("BACKUP_RETENTION_DAYS", "10")
	if _, err := b.run(t, "database", command.PointScheduled); err != nil {
		t.Fatal(err)
	}
	b.clock = b.clock.Add(4 * 24 * time.Hour)
	if res, _ := b.run(t, "database", command.PointScheduled); !res.Unchanged {
		t.Fatal("4 days old: unchanged")
	}
	b.clock = b.clock.Add(2 * 24 * time.Hour)
	res, err := b.run(t, "database", command.PointScheduled)
	if err != nil || res.Unchanged || slices.Contains(b.helper.last(), "--since") {
		t.Fatalf("6 days old: %+v, %v, %v", res, err, b.helper.last())
	}
}

// After a run, retention prunes: here the tiers keep only the newest copy.
func TestRunBackupPrunes(t *testing.T) {
	b := newBackupRepo(t)
	if _, err := saveConfig(t, b.Repository, "app-1", backupCompose, withBackups(
		`[{"name":"database","enabled":true,"storage":"disk","keep":{"recent":"1h","daily":0,"monthly":0}}]`)); err != nil {
		t.Fatal(err)
	}
	b.Repository.forgetApp("app-1")
	for i := range 3 {
		b.helper.fp = "sqlite:" + string(rune('a'+i))
		if _, err := b.run(t, "database", command.PointScheduled); err != nil {
			t.Fatal(err)
		}
		b.clock = b.clock.Add(2 * time.Hour)
	}
	if got := b.stored(t); len(got) != 2 || !strings.HasPrefix(got[0], "app-1/database/20261011T160000Z") {
		t.Fatalf("stored = %v", got)
	}
}

func TestRunBackupFailures(t *testing.T) {
	b := newBackupRepo(t)
	// Missing database: the helper's reason, no object left behind.
	b.helper.exit, b.helper.stderr = helper.ExitMissing, `{"error":"database file not found: /src/0/app.db"}`
	if _, err := b.run(t, "database", command.PointScheduled); err == nil || err.Error() != "database file not found: /src/0/app.db" {
		t.Fatalf("missing: %v", err)
	}
	// Docker failing before the helper ran: its own words.
	b.helper.exit, b.helper.stderr = 125, "docker: Error response from daemon: mount denied"
	if _, err := b.run(t, "database", command.PointScheduled); err == nil || !strings.Contains(err.Error(), "exited with 125: docker: Error response from daemon: mount denied") {
		t.Fatalf("docker error: %v", err)
	}
	b.helper.exit, b.helper.err = -1, errors.New("stopped after its time limit: context deadline exceeded")
	if _, err := b.run(t, "database", command.PointScheduled); err == nil || !strings.Contains(err.Error(), "the backup helper: stopped after its time limit") {
		t.Fatalf("killed: %v", err)
	}
	if got := b.stored(t); len(got) != 0 {
		t.Fatalf("a failed run left %v", got)
	}
	b.helper.exit, b.helper.err = 0, nil

	// Not runnable.
	for _, tc := range []struct{ name, want string }{
		{"replica", "the backup replica is off"},
		{"nope", "the app has no backup nope"},
	} {
		if _, err := b.run(t, tc.name, command.PointManual); !errors.Is(err, ErrNotRunnable) || err.Error() != tc.want {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if _, err := saveConfig(t, b.Repository, "app-1", backupCompose, withBackups(`[{"name":"replica","enabled":true,"storage":"disk"}]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.run(t, "replica", command.PointManual); err == nil || !strings.Contains(err.Error(), "is a custom backup") {
		t.Fatalf("custom: %v", err)
	}
	if _, err := b.RunBackup(context.Background(), BackupJob{AppID: "gone", Backup: "database"}); !errors.Is(err, ErrNotRunnable) {
		t.Fatalf("a missing app: %v", err)
	}
}

func TestRunBackupDockerChecks(t *testing.T) {
	b := newBackupRepo(t)
	pulls := 0
	b.Repository.docker = func(_ context.Context, args ...string) error {
		switch {
		case args[0] == "image":
			return errors.New("no such image")
		case args[0] == "pull":
			if pulls++; pulls > 1 {
				return nil
			}
			return errors.New("network unreachable")
		case args[0] == "volume":
			return errors.New("no such volume")
		}
		return nil
	}
	if _, err := b.run(t, "database", command.PointManual); err == nil || !strings.Contains(err.Error(), "pull the backup helper image alpine:3.20: network unreachable") {
		t.Fatalf("pull: %v", err)
	}
	if _, err := b.run(t, "database", command.PointManual); err == nil || !strings.Contains(err.Error(), "the volume wf-app-1_data does not exist") {
		t.Fatalf("volume: %v", err)
	}
	// A storage the server lost.
	if _, err := b.storages.Delete("disk"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.run(t, "database", command.PointManual); err == nil || err.Error() != "there is no storage disk on this server" {
		t.Fatalf("storage: %v", err)
	}
}

// failingPut is a store whose uploads are refused.
type failingPut struct{ store.Store }

func (failingPut) Put(context.Context, string, io.Reader) error { return errors.New("Access Denied") }

// An upload that fails is the reported cause (not the helper it stopped).
func TestRunBackupUploadFails(t *testing.T) {
	b := newBackupRepo(t)
	b.Repository.openStorage = func(s model.Storage, secret string) (store.Store, error) {
		st, err := store.Open(s, secret)
		return failingPut{st}, err
	}
	if _, err := b.run(t, "database", command.PointManual); err == nil || err.Error() != "upload the copy: Access Denied" {
		t.Fatalf("err = %v", err)
	}
}

func TestHelperArgsQuoteMountFields(t *testing.T) {
	plan := helperPlan{Mounts: []helperMount{{Bind: `/srv/a,b"c`}}, Paths: []helperPath{{Sub: "x", Entry: "x"}}}
	args := helperArgs("n", "app-1", "/opt/wf,1/winterflow", plan, recipe.EffectiveBackup{Type: recipe.TypeFiles}, "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, `--mount type=bind,"source=/srv/a,b""c",target=/src/0,readonly`) ||
		!strings.Contains(joined, `--mount type=bind,"source=/opt/wf,1/winterflow",target=/wf,readonly`) {
		t.Fatalf("args = %s", joined)
	}
}

func TestRestorePoints(t *testing.T) {
	b := newBackupRepo(t)
	for i := range 2 {
		b.helper.fp = "sqlite:" + string(rune('a'+i))
		if _, err := b.run(t, "database", command.PointManual); err != nil {
			t.Fatal(err)
		}
		b.clock = b.clock.Add(time.Minute)
	}
	// An incomplete point (no manifest) is not listed.
	if err := os.WriteFile(filepath.Join(b.disk, "app-1/database/20261011T130000Z-scheduled.db.zst"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	points, err := b.ListRestorePoints(context.Background(), "app-1", "database")
	if err != nil || len(points) != 2 || points[0].ID != "20261011T120100Z-manual" || points[0].Kind != command.PointManual {
		t.Fatalf("points = %+v, %v", points, err)
	}
	if err := b.DeleteRestorePoint(context.Background(), "app-1", "database", points[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := b.DeleteRestorePoint(context.Background(), "app-1", "database", "20261011T130000Z-scheduled"); err != nil {
		t.Fatalf("an incomplete point can be deleted too: %v", err)
	}
	points, _ = b.ListRestorePoints(context.Background(), "app-1", "database")
	if len(points) != 1 || len(b.stored(t)) != 2 {
		t.Fatalf("after delete: %+v, %v", points, b.stored(t))
	}
	if err := b.DeleteRestorePoint(context.Background(), "app-1", "database", "20200101T000000Z-manual"); err == nil ||
		!strings.Contains(err.Error(), "has no restore point 20200101T000000Z-manual") {
		t.Fatalf("missing point: %v", err)
	}
	for _, tc := range []struct{ app, name, want string }{
		{"app-1", "replica", "is a custom backup"},
		{"app-1", "nope", "the app has no backup nope"},
		{"gone", "database", "app gone not found"},
	} {
		if _, err := b.ListRestorePoints(context.Background(), tc.app, tc.name); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s/%s: %v", tc.app, tc.name, err)
		}
	}
	if _, err := saveConfig(t, b.Repository, "app-1", backupCompose, withBackups(`[]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ListRestorePoints(context.Background(), "app-1", "database"); err == nil || !strings.Contains(err.Error(), "has no storage") {
		t.Fatalf("no storage: %v", err)
	}
}

// A storage that never answers fails the listing at its deadline instead of
// holding the command pipeline.
func TestWithDeadlineAbandonsAHungCall(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	err := withDeadline(context.Background(), 20*time.Millisecond, "list", func(context.Context) error { <-release; return nil })
	if err == nil || !strings.Contains(err.Error(), "the storage did not answer within 20ms") {
		t.Fatalf("err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := withDeadline(ctx, time.Second, "list", func(context.Context) error { <-release; return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

// countingStore counts the uploads made through it and can fail chosen calls.
type countingStore struct {
	store.Store
	puts      *atomic.Int32
	failPut   func(key string) error
	failDelet error
}

func (c countingStore) Put(ctx context.Context, key string, r io.Reader) error {
	c.puts.Add(1)
	if c.failPut != nil {
		if err := c.failPut(key); err != nil {
			return err
		}
	}
	return c.Store.Put(ctx, key, r)
}

func (c countingStore) Delete(ctx context.Context, keys ...string) error {
	if c.failDelet != nil {
		return c.failDelet
	}
	return c.Store.Delete(ctx, keys...)
}

func (b *backupRepo) wrapStore(c countingStore) {
	b.Repository.openStorage = func(s model.Storage, secret string) (store.Store, error) {
		st, err := store.Open(s, secret)
		c.Store = st
		return c, err
	}
}

// An unchanged run, or one that fails before the helper writes anything,
// never touches the storage's upload.
func TestRunBackupUnchangedMakesNoUpload(t *testing.T) {
	b := newBackupRepo(t)
	puts := &atomic.Int32{}
	b.wrapStore(countingStore{puts: puts})
	if _, err := b.run(t, "database", command.PointScheduled); err != nil {
		t.Fatal(err)
	}
	if puts.Load() != 2 { // the data, then the manifest
		t.Fatalf("puts = %d", puts.Load())
	}
	b.clock = b.clock.Add(time.Hour)
	// The helper's warnings reach the result of an unchanged run too.
	if res, err := b.run(t, "database", command.PointScheduled); err != nil || !res.Unchanged ||
		len(res.Warnings) != 1 || res.Warnings[0] != "a note" {
		t.Fatalf("unchanged = %+v, %v", res, err)
	}
	b.helper.exit, b.helper.stderr = helper.ExitMissing, `{"error":"database file not found"}`
	if _, err := b.run(t, "database", command.PointScheduled); err == nil {
		t.Fatal("a missing database succeeded")
	}
	if puts.Load() != 2 {
		t.Fatalf("a run that wrote nothing uploaded: puts = %d", puts.Load())
	}
}

// A helper that fails with data already streamed leaves no object (and no
// temporary file), and its warnings still reach the result.
func TestRunBackupAbortsAnUploadInFlight(t *testing.T) {
	b := newBackupRepo(t)
	b.helper.junk, b.helper.exit = 1<<20, 1
	b.helper.stderr = `{"error":"read failed","warnings":["a file changed"]}`
	res, err := b.run(t, "database", command.PointScheduled)
	if err == nil || err.Error() != "read failed" {
		t.Fatalf("err = %v", err)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != "a file changed" {
		t.Fatalf("warnings = %v", res.Warnings)
	}
	if got := b.stored(t); len(got) != 0 {
		t.Fatalf("a failed run left %v", got)
	}
}

// Exit 0 without a result line is a failure: the fingerprint would be empty
// and the backup would never be seen as unchanged again.
func TestRunBackupNeedsTheHelpersResult(t *testing.T) {
	b := newBackupRepo(t)
	b.helper.noReply = true
	if _, err := b.run(t, "database", command.PointScheduled); err == nil || err.Error() != "the backup helper reported no result" {
		t.Fatalf("err = %v", err)
	}
	if got := b.stored(t); len(got) != 0 {
		t.Fatalf("left %v", got)
	}
}

// A manifest that can't be written leaves an incomplete point: not listed.
func TestRunBackupManifestWriteFails(t *testing.T) {
	b := newBackupRepo(t)
	b.wrapStore(countingStore{puts: &atomic.Int32{}, failPut: func(key string) error {
		if strings.HasSuffix(key, ".json") {
			return errors.New("quota exceeded")
		}
		return nil
	}})
	if _, err := b.run(t, "database", command.PointScheduled); err == nil || err.Error() != "write the manifest: quota exceeded" {
		t.Fatalf("err = %v", err)
	}
	b.Repository.openStorage = store.Open
	points, err := b.ListRestorePoints(context.Background(), "app-1", "database")
	if err != nil || len(points) != 0 {
		t.Fatalf("points = %+v, %v", points, err)
	}
}

// A retention failure doesn't fail the run: the new copy is stored, and the
// failure is a warning.
func TestRunBackupRetentionFailureIsAWarning(t *testing.T) {
	b := newBackupRepo(t)
	if _, err := saveConfig(t, b.Repository, "app-1", backupCompose, withBackups(
		`[{"name":"database","enabled":true,"storage":"disk","keep":{"recent":"1h","daily":0,"monthly":0}}]`)); err != nil {
		t.Fatal(err)
	}
	b.Repository.forgetApp("app-1")
	b.wrapStore(countingStore{puts: &atomic.Int32{}, failDelet: errors.New("read-only storage")})
	var res BackupResult
	for i := range 2 {
		b.helper.fp = "sqlite:" + string(rune('a'+i))
		var err error
		if res, err = b.run(t, "database", command.PointScheduled); err != nil {
			t.Fatal(err)
		}
		b.clock = b.clock.Add(2 * time.Hour)
	}
	if res.Point == nil || len(res.Warnings) != 2 || res.Warnings[1] != "retention: read-only storage" {
		t.Fatalf("result = %+v", res)
	}
}

func TestWithDeadlineValue(t *testing.T) {
	v, err := withDeadlineValue(context.Background(), time.Second, "x", func(context.Context) ([]int, error) { return []int{1}, nil })
	if err != nil || len(v) != 1 {
		t.Fatalf("v = %v, %v", v, err)
	}
	release := make(chan struct{})
	defer close(release)
	v, err = withDeadlineValue(context.Background(), 20*time.Millisecond, "x", func(context.Context) ([]int, error) { <-release; return []int{1}, nil })
	if err == nil || v != nil {
		t.Fatalf("hung: %v, %v", v, err)
	}
}

// A folder backup also carries the mounts nested below it, with the excludes
// applying to the whole copy; a nested mount left out is reported in the
// result.
func TestRunBackupCarriesNestedMountsAndReportsSkippedOnes(t *testing.T) {
	b := newBackupRepo(t)
	nested := `{"type": "bind", "source": "/data/apps-data/app-1/media", "target": "/app/media", "bind": {}},
        {"type": "bind", "source": "/mnt/nas/thumbs", "target": "/app/media/thumbs", "bind": {}},
        {"type": "tmpfs", "target": "/app/media/cache"},`
	(&fakeComposeConfig{out: strings.Replace(configJSON,
		`{"type": "bind", "source": "/data/apps-data/app-1/media", "target": "/app/media", "bind": {}},`, nested, 1)}).install(b.Repository)
	res, err := b.run(t, "media", command.PointManual)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(b.helper.last(), " ")
	if !strings.Contains(args, "target=/src/1,readonly") ||
		!strings.HasSuffix(args, "/wf backup dump files --src /src/0 --entry app/media --src /src/1 --entry app/media/thumbs --exclude *.tmp") {
		t.Fatalf("args = %s", args)
	}
	if len(res.Warnings) != 2 || !strings.Contains(res.Warnings[0], "/app/media/cache is on a tmpfs mount") || res.Warnings[1] != "a note" {
		t.Fatalf("warnings = %q", res.Warnings)
	}
}

// blockingStore's uploads read a little and then stall on their context, as a
// network write to a hung endpoint does.
type blockingStore struct {
	store.Store
	cancelled chan time.Time
}

func (b blockingStore) Put(ctx context.Context, _ string, r io.Reader) error {
	_, _ = io.ReadFull(r, make([]byte, 1024))
	<-ctx.Done()
	b.cancelled <- time.Now()
	return ctx.Err()
}

// A stalled upload does not hold the run past its deadline: the helper's
// output copier (which ignores the context) is released at once, and the
// upload's own context is cancelled after the grace.
func TestRunBackupStalledUploadIsBoundedByTheRunDeadline(t *testing.T) {
	b := newBackupRepo(t)
	old := putAbortGrace
	putAbortGrace = 200 * time.Millisecond
	t.Cleanup(func() { putAbortGrace = old })
	cancelled := make(chan time.Time, 1)
	st := blockingStore{cancelled: cancelled}
	b.runHelper = func(_ context.Context, _ string, _ []string, out io.Writer) (int, []byte, error) {
		buf := make([]byte, 1<<20)
		rand.New(rand.NewSource(2)).Read(buf)
		for { // like the os/exec copier: ignores the context, ends on a write error
			if _, err := out.Write(buf); err != nil {
				return -1, nil, err
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := b.dumpAndUpload(ctx, st, "app-1/database/x.db.zst", "n", nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a stalled run succeeded")
		}
		if d := time.Since(started); d > time.Second {
			t.Fatalf("returned %s after the start, deadline was 100ms", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the run is still blocked 5s after its deadline")
	}
	select {
	case at := <-cancelled:
		if at.Sub(started) < 250*time.Millisecond {
			t.Fatalf("the upload was cancelled at %s, before the grace", at.Sub(started))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the upload's context was never cancelled")
	}
	if got := b.stored(t); len(got) != 0 {
		t.Fatalf("left %v", got)
	}
}

// A sqlite backup's helper is told the database's name in the backup, so its
// messages don't name the helper-internal /src/<n> path.
func TestHelperArgsNameTheSQLiteEntry(t *testing.T) {
	plan := helperPlan{Mounts: []helperMount{{Volume: "wf-app-1_data"}}, Paths: []helperPath{{Sub: "db/app.db", Entry: "db/app.db"}}}
	args := helperArgs("n", "app-1", "/opt/wf", plan, recipe.EffectiveBackup{Type: recipe.TypeSQLite}, "fp")
	joined := strings.Join(args, " ")
	if !strings.HasSuffix(joined, "/wf backup dump sqlite /src/0/db/app.db --entry db/app.db --since fp") {
		t.Fatalf("args = %s", joined)
	}
}

// Two copies of the same kind made in the same second never overwrite each
// other: the second takes the next free second (and the first keeps its data).
func TestRunBackupsInTheSameSecondKeepBothCopies(t *testing.T) {
	b := newBackupRepo(t)
	first, err := b.run(t, "database", command.PointManual)
	if err != nil || first.Point == nil {
		t.Fatalf("first: %+v, %v", first, err)
	}
	b.helper.payload, b.helper.fp = "CHANGED", "sqlite:2" // same clock, other data
	second, err := b.run(t, "database", command.PointManual)
	if err != nil || second.Point == nil {
		t.Fatalf("second: %+v, %v", second, err)
	}
	if first.Point.ID != "20261011T120000Z-manual" || second.Point.ID != "20261011T120001Z-manual" {
		t.Fatalf("ids = %s, %s", first.Point.ID, second.Point.ID)
	}
	want := []string{
		"app-1/database/20261011T120000Z-manual.db.zst", "app-1/database/20261011T120000Z-manual.json",
		"app-1/database/20261011T120001Z-manual.db.zst", "app-1/database/20261011T120001Z-manual.json",
	}
	if got := b.stored(t); !slices.Equal(got, want) {
		t.Fatalf("stored = %v", got)
	}
	data, _ := os.ReadFile(filepath.Join(b.disk, want[0]))
	dec, _ := zstd.NewReader(bytes.NewReader(data))
	if plain, _ := io.ReadAll(dec); string(plain) != "SQLITE-SNAPSHOT" {
		t.Fatalf("the first copy now holds %q", plain)
	}
}
