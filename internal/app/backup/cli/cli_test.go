package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// run calls Main and decodes stderr's last line.
func run(t *testing.T, args ...string) (int, []byte, Result) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Main(args, &stdout, &stderr)
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	var res Result
	if code != ExitUsage {
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil {
			t.Fatalf("stderr's last line is not a result: %q", stderr.String())
		}
	}
	return code, stdout.Bytes(), res
}

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func count(t *testing.T, path string) int {
	t.Helper()
	var n int
	if err := openDB(t, path).QueryRow("SELECT count(*) FROM items").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A copy taken while a second connection keeps writing in WAL mode is
// consistent and holds at least every row committed before it started.
func TestDumpSQLiteWhileWriting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	path := filepath.Join(dir, "app.db")
	db := openDB(t, path)
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, body TEXT)"); err != nil {
		t.Fatal(err)
	}
	var written atomic.Int64
	insert := func() {
		if _, err := db.Exec("INSERT INTO items (body) VALUES (?)", strings.Repeat("x", 512)); err != nil {
			t.Error(err)
			return
		}
		written.Add(1)
	}
	for range 500 {
		insert()
	}
	ctx, stop := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() {
		for ctx.Err() == nil {
			insert()
		}
	})
	before := written.Load()
	code, out, res := run(t, "dump", "sqlite", path)
	stop()
	wg.Wait()
	if code != ExitOK || res.Error != "" {
		t.Fatalf("code %d, result %+v", code, res)
	}
	if res.RawSize != int64(len(out)) || !strings.HasPrefix(res.Fingerprint, "sqlite:") {
		t.Fatalf("result = %+v (stdout %d bytes)", res, len(out))
	}
	copyPath := filepath.Join(t.TempDir(), "copy.db")
	if err := os.WriteFile(copyPath, out, 0o600); err != nil {
		t.Fatal(err)
	}
	if n := count(t, copyPath); int64(n) < before || int64(n) > written.Load() {
		t.Fatalf("copy has %d rows; %d were written before the dump, %d in all", n, before, written.Load())
	}
	// The snapshot temp file is gone.
	if left, _ := filepath.Glob(filepath.Join(os.Getenv("TMPDIR"), "wf-snap-*")); len(left) != 0 {
		t.Fatalf("snapshot left behind: %v", left)
	}
}

func TestDumpSQLiteUnchangedAndMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	db := openDB(t, path)
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	code, _, res := run(t, "dump", "sqlite", path)
	if code != ExitOK {
		t.Fatalf("first dump: %d %+v", code, res)
	}
	code, out, again := run(t, "dump", "sqlite", path, "--since", res.Fingerprint)
	if code != ExitUnchanged || !again.Unchanged || len(out) != 0 {
		t.Fatalf("unchanged: code %d, %+v, %d bytes", code, again, len(out))
	}
	if _, err := db.Exec("INSERT INTO items DEFAULT VALUES"); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, "dump", "sqlite", path, "--since", res.Fingerprint); code != ExitOK {
		t.Fatalf("a write must change the fingerprint: %d", code)
	}
	code, _, missing := run(t, "dump", "sqlite", filepath.Join(dir, "nope.db"))
	if code != ExitMissing || !strings.Contains(missing.Error, "database file not found") || strings.HasSuffix(missing.Error, "missing") {
		t.Fatalf("missing: %d %+v", code, missing)
	}
	if _, err := os.Stat(filepath.Join(dir, "nope.db")); !os.IsNotExist(err) {
		t.Fatal("a dump created the missing database")
	}
}

func TestDumpSQLiteRefusesWhatIsNotADatabase(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string][]byte{
		"text.db":  []byte("this is not a database, just some text that is long enough to have a header....."),
		"empty.db": nil,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		code, out, res := run(t, "dump", "sqlite", path)
		if code != ExitFailed || res.Error == "" || len(out) != 0 {
			t.Errorf("%s: code %d, %+v, %d bytes", name, code, res, len(out))
		}
	}
	if code, _, res := run(t, "dump", "sqlite", dir); code != ExitFailed || !strings.Contains(res.Error, "is not a file") {
		t.Fatalf("a folder: %d %+v", code, res)
	}
}

// files lays out two mounts: /src/0 (a volume: media + cache + a FIFO) and
// /src/1 (config).
func filesTree(t *testing.T) (src0, src1 string) {
	t.Helper()
	root := t.TempDir()
	src0, src1 = filepath.Join(root, "0"), filepath.Join(root, "1")
	mtime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, f := range []struct {
		path string
		mode os.FileMode
		body string
	}{
		{"0/media/a.jpg", 0o640, "jpeg"},
		{"0/media/sub/b.txt", 0o600, "bee"},
		{"0/media/skip.tmp", 0o644, "tmp"},
		{"0/media/cache/c.bin", 0o644, "cached"},
		{"0/other.txt", 0o644, "not backed up"},
		{"1/config/app.yml", 0o644, "k: v"},
	} {
		p := filepath.Join(root, f.path)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.body), f.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, f.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("a.jpg", filepath.Join(src0, "media", "link.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(src0, "media", "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	return src0, src1
}

func readTar(t *testing.T, raw []byte) map[string]*tar.Header {
	t.Helper()
	out := map[string]*tar.Header{}
	tr := tar.NewReader(bytes.NewReader(raw))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		h.PAXRecords = map[string]string{"body": string(body)}
		out[h.Name] = h
	}
}

func TestDumpFiles(t *testing.T) {
	src0, src1 := filesTree(t)
	args := []string{"dump", "files",
		"--src", src0 + "/media", "--entry", "app/media", "--src", src1 + "/config", "--entry", "data/config",
		"--exclude", "/app/media/cache/", "--exclude", "*.tmp"}
	code, out, res := run(t, args...)
	if code != ExitOK || res.RawSize != int64(len(out)) || !strings.HasPrefix(res.Fingerprint, "files:") {
		t.Fatalf("code %d, %+v", code, res)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "/app/media/pipe: skipped (a named pipe)") {
		t.Fatalf("warnings = %v", res.Warnings)
	}
	got := readTar(t, out)
	names := []string{}
	for n := range got {
		names = append(names, n)
	}
	for _, want := range []string{"app/media/", "app/media/a.jpg", "app/media/sub/", "app/media/sub/b.txt", "app/media/link.jpg", "data/config/", "data/config/app.yml"} {
		if got[want] == nil {
			t.Errorf("%s missing from %v", want, names)
		}
	}
	for _, not := range []string{"app/media/skip.tmp", "app/media/cache/", "app/media/cache/c.bin", "other.txt", "app/media/pipe"} {
		if got[not] != nil {
			t.Errorf("%s should not be archived", not)
		}
	}
	a := got["app/media/a.jpg"]
	if a.Mode&0o777 != 0o640 || !a.ModTime.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) ||
		a.Uid != os.Getuid() || a.Gid != os.Getgid() || a.PAXRecords["body"] != "jpeg" || a.Uname != "" {
		t.Fatalf("a.jpg header = %+v", a)
	}
	if l := got["app/media/link.jpg"]; l.Typeflag != tar.TypeSymlink || l.Linkname != "a.jpg" {
		t.Fatalf("link = %+v", l)
	}
	if d := got["app/media/sub/"]; d.Typeflag != tar.TypeDir || d.Mode&0o777 != 0o750 {
		t.Fatalf("dir = %+v", d)
	}

	// Unchanged, then changed by a touch.
	code, out, again := run(t, append(args, "--since", res.Fingerprint)...)
	if code != ExitUnchanged || !again.Unchanged || len(out) != 0 {
		t.Fatalf("unchanged: %d %+v", code, again)
	}
	later := time.Now()
	if err := os.Chtimes(filepath.Join(src1, "config", "app.yml"), later, later); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, append(args, "--since", res.Fingerprint)...); code != ExitOK {
		t.Fatalf("a touched file must change the fingerprint: %d", code)
	}
	// An excluded file changing does not.
	_, _, res = run(t, args...)
	if err := os.WriteFile(filepath.Join(src0, "media", "skip.tmp"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, append(args, "--since", res.Fingerprint)...); code != ExitUnchanged {
		t.Fatalf("an excluded file changed the fingerprint: %d", code)
	}
}

// A whole volume (entry "") archives names relative to the volume root.
func TestDumpFilesWholeVolume(t *testing.T) {
	src0, _ := filesTree(t)
	code, out, res := run(t, "dump", "files", "--src", src0, "--entry", "", "--exclude", "media/")
	if code != ExitOK {
		t.Fatalf("%d %+v", code, res)
	}
	got := readTar(t, out)
	if len(got) != 1 || got["other.txt"] == nil {
		t.Fatalf("archive = %v", got)
	}
}

func TestDumpFilesMissingPaths(t *testing.T) {
	src0, _ := filesTree(t)
	code, out, res := run(t, "dump", "files", "--src", src0+"/media/a.jpg", "--entry", "app/media/a.jpg", "--src", src0+"/gone", "--entry", "app/gone")
	if code != ExitOK || len(res.Warnings) != 1 || res.Warnings[0] != "/app/gone: not found" {
		t.Fatalf("partly missing: %d %+v", code, res)
	}
	if got := readTar(t, out); len(got) != 1 || got["app/media/a.jpg"] == nil {
		t.Fatalf("archive = %v", got)
	}
	code, _, res = run(t, "dump", "files", "--src", src0+"/gone", "--entry", "app/gone", "--src", src0+"/nope", "--entry", "")
	if code != ExitMissing || res.Error != "none of the backup's paths exist" || len(res.Warnings) != 2 || res.Warnings[1] != "the volume: not found" {
		t.Fatalf("all missing: %d %+v", code, res)
	}
}

func TestExcluded(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dir     bool
		pattern string
		want    bool
	}{
		{"app/media/cache", true, "/app/media/cache/", true},
		{"app/media/cache", false, "/app/media/cache/", false},
		{"app/x.tmp", false, "*.tmp", true},
		{"app/sub/x.tmp", false, "app/*.tmp", false},
		{"app/x.tmp", false, "app/*.tmp", true},
		{"app/x.txt", false, "*.tmp", false},
		{"app", true, "/", false},
	} {
		if got := excluded(tc.name, tc.dir, []string{tc.pattern}); got != tc.want {
			t.Errorf("%s (dir %v) ~ %q = %v", tc.name, tc.dir, tc.pattern, got)
		}
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{
		nil, {"dump"}, {"restore", "sqlite"}, {"dump", "postgres"},
		{"dump", "sqlite"}, {"dump", "sqlite", "--since", "x"}, {"dump", "sqlite", "/a", "extra"},
		{"dump", "files"}, {"dump", "files", "--src", "relative", "--entry", "x"}, {"dump", "files", "--src", "/a"}, {"dump", "files", "--entry", "x"}, {"dump", "files", "--src", "/a", "--entry", "x", "--entry", "y"},
	} {
		var stderr bytes.Buffer
		if code := Main(args, io.Discard, &stderr); code != ExitUsage || !strings.Contains(stderr.String(), "usage:") {
			t.Errorf("%v: code %d, stderr %q", args, code, stderr.String())
		}
	}
}

func TestWarningsAreCapped(t *testing.T) {
	dir := t.TempDir()
	for i := range maxWarnings + 5 {
		if err := syscall.Mkfifo(filepath.Join(dir, "p"+strings.Repeat("x", i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, res := run(t, "dump", "files", "--src", dir, "--entry", "")
	if len(res.Warnings) != maxWarnings+1 || res.Warnings[maxWarnings] != "and 5 more" {
		t.Fatalf("warnings = %d: %v", len(res.Warnings), res.Warnings[len(res.Warnings)-1])
	}
}

// A file that shrinks between the walk and its copy is padded so the
// archive stays readable.
func TestWriteEntryPadsAShrunkFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(p)
	if err := os.WriteFile(p, []byte("01234"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	warning, err := writeEntry(tw, entry{name: "f", path: p, info: info})
	if err != nil || !strings.Contains(warning, "changed while it was archived") {
		t.Fatalf("warning %q, err %v", warning, err)
	}
	_ = tw.Close()
	if got := readTar(t, buf.Bytes())["f"]; got.Size != 10 || got.PAXRecords["body"] != "01234\x00\x00\x00\x00\x00" {
		t.Fatalf("entry = %+v", got)
	}
	// A file gone before its copy is skipped.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if warning, err := writeEntry(tw, entry{name: "f", path: p, info: info}); err != nil || !strings.Contains(warning, "skipped") {
		t.Fatalf("vanished: %q %v", warning, err)
	}
}

// Any character of a path survives the protocol: "=" and spaces are legal in
// recipe paths and must not split or lose a source.
func TestDumpFilesPathWithEqualsAndSpace(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "date=2026-01-01 and more")
	if err := os.MkdirAll(src, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "f=1 x.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, res := run(t, "dump", "files", "--src", src, "--entry", "app/a=b c")
	if code != ExitOK || len(res.Warnings) != 0 {
		t.Fatalf("%d %+v", code, res)
	}
	got := readTar(t, out)
	if got["app/a=b c/f=1 x.txt"] == nil || got["app/a=b c/"] == nil {
		t.Fatalf("archive = %v", got)
	}
}

// The walk streams: a big tree is fingerprinted and archived without
// keeping its entries (the helper has 256 MiB), and the second run is
// recognized as unchanged.
func TestDumpFilesManyFiles(t *testing.T) {
	root := t.TempDir()
	const dirs, perDir = 20, 1000
	for d := range dirs {
		dir := filepath.Join(root, "d"+strings.Repeat("x", d))
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		for i := range perDir {
			if err := os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)), []byte{'x'}, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	code, out, res := run(t, "dump", "files", "--src", root, "--entry", "")
	if code != ExitOK || res.RawSize != int64(len(out)) {
		t.Fatalf("%d %+v", code, res)
	}
	n := 0
	for tr := tar.NewReader(bytes.NewReader(out)); ; n++ {
		if _, err := tr.Next(); err != nil {
			break
		}
	}
	if n != dirs*(perDir+1) {
		t.Fatalf("%d entries archived", n)
	}
	if code, _, _ := run(t, "dump", "files", "--src", root, "--entry", "", "--since", res.Fingerprint); code != ExitUnchanged {
		t.Fatalf("second run: %d", code)
	}
}

// A file swapped for a FIFO or a symlink between the walk and its copy is
// skipped, never opened blocking or followed.
func TestWriteEntrySkipsASwappedFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(p)
	for name, swap := range map[string]func() error{
		"fifo":    func() error { return syscall.Mkfifo(p, 0o600) },
		"symlink": func() error { return os.Symlink("/etc/hostname", p) },
	} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		if err := swap(); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		done := make(chan struct{})
		go func() {
			defer close(done)
			warning, err := writeEntry(tw, entry{name: "f", path: p, info: info})
			if err != nil || !strings.Contains(warning, "skipped") {
				t.Errorf("%s: %q %v", name, warning, err)
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: writeEntry blocked", name)
		}
		_ = tw.Close()
		if got := readTar(t, buf.Bytes()); len(got) != 0 {
			t.Fatalf("%s: archived %v", name, got)
		}
	}
}
