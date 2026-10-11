package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// restore calls Run with stdin and decodes stderr's last line.
func restore(t *testing.T, stdin []byte, args ...string) (int, Result) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(append([]string{"restore"}, args...), bytes.NewReader(stdin), &stdout, &stderr)
	if stdout.Len() != 0 {
		t.Fatalf("a restore wrote to stdout: %q", stdout.String())
	}
	var res Result
	if code != ExitUsage {
		lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil {
			t.Fatalf("stderr's last line is not a result: %q", stderr.String())
		}
	}
	return code, res
}

// chowns records the owners restore gives files, for the rest of the test.
type chownLog struct {
	mu    sync.Mutex
	calls map[string][2]int
}

func recordChowns(t *testing.T) *chownLog {
	t.Helper()
	log := &chownLog{calls: map[string][2]int{}}
	prev := lchown
	lchown = func(p string, uid, gid int) error {
		log.mu.Lock()
		log.calls[filepath.Base(p)] = [2]int{uid, gid}
		log.mu.Unlock()
		return nil
	}
	t.Cleanup(func() { lchown = prev })
	return log
}

func (l *chownLog) of(name string) ([2]int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.calls[name]
	return c, ok
}

// A copy dumped while the app writes restores over a newer database: the
// rows are the copy's, the old -wal and -shm are gone, and the file keeps
// the owner and mode of the one it replaced.
func TestRestoreSQLite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	db := openDB(t, path)
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, body TEXT)"); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		if _, err := db.Exec("INSERT INTO items (body) VALUES ('x')"); err != nil {
			t.Fatal(err)
		}
	}
	code, copied, res := run(t, "dump", "sqlite", path)
	if code != ExitOK {
		t.Fatalf("dump: %d %+v", code, res)
	}
	for range 25 {
		if _, err := db.Exec("INSERT INTO items (body) VALUES ('y')"); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(path+ext, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	chowns := recordChowns(t)
	code, res = restore(t, copied, "sqlite", path)
	if code != ExitOK || res.RawSize != int64(len(copied)) {
		t.Fatalf("restore: %d %+v", code, res)
	}
	if n := count(t, path); n != 50 {
		t.Fatalf("restored database has %d rows, want 50", n)
	}
	for _, ext := range []string{".restore-tmp", ".restore-tmp-wal", ".restore-tmp-shm"} {
		if _, err := os.Stat(path + ext); !os.IsNotExist(err) {
			t.Fatalf("%s left behind", ext)
		}
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640", info.Mode())
	}
	if c, ok := chowns.of("app.db.restore-tmp"); !ok || c != [2]int{os.Getuid(), os.Getgid()} {
		t.Fatalf("owner given = %v (%v)", c, ok)
	}
}

// With no database file yet, the copy takes the folder's owner and a mode
// without its execute bits.
func TestRestoreSQLiteIntoAnEmptyFolder(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.db")
	db := openDB(t, src)
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY); INSERT INTO items DEFAULT VALUES"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	recordChowns(t)
	path := filepath.Join(dir, "new.db")
	if code, res := restore(t, raw, "sqlite", path); code != ExitOK {
		t.Fatalf("%d %+v", code, res)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 || count(t, path) != 1 {
		t.Fatalf("mode = %v", info.Mode())
	}
}

// Corrupt, empty and non-SQLite input is refused and the database is left
// as it was.
func TestRestoreSQLiteRefusesBadInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	db := openDB(t, path)
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY); INSERT INTO items DEFAULT VALUES"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	good, _ := os.ReadFile(path)
	empty := filepath.Join(t.TempDir(), "empty.db")
	edb := openDB(t, empty)
	if _, err := edb.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	edb.Close()
	noTables, _ := os.ReadFile(empty)
	corrupt := append([]byte(nil), good...)
	for i := 100; i < len(corrupt); i++ {
		corrupt[i] ^= 0x5a
	}
	recordChowns(t)
	for name, in := range map[string][]byte{
		"nothing":   nil,
		"text":      []byte(strings.Repeat("not a database ", 100)),
		"corrupt":   corrupt,
		"no tables": noTables,
	} {
		code, res := restore(t, in, "sqlite", path)
		if code != ExitFailed || res.Error == "" {
			t.Errorf("%s: %d %+v", name, code, res)
		}
		if now, _ := os.ReadFile(path); !bytes.Equal(now, good) {
			t.Fatalf("%s: the database changed", name)
		}
		if left, _ := filepath.Glob(path + ".restore-tmp*"); len(left) != 0 {
			t.Fatalf("%s: left %v", name, left)
		}
	}
	if code, res := restore(t, good, "sqlite", dir, "--entry", "data/app.db"); code != ExitFailed || res.Error != "/data/app.db is not a file" {
		t.Fatalf("a folder: %d %+v", code, res)
	}
	if code, res := restore(t, good, "sqlite", filepath.Join(dir, "gone", "app.db"), "--entry", "/data/gone/app.db"); code != ExitFailed ||
		res.Error != "the folder of /data/gone/app.db: no such file or directory" {
		t.Fatalf("no folder: %d %+v", code, res)
	}
}

func TestRestoreSQLiteUsage(t *testing.T) {
	for _, args := range [][]string{{"sqlite"}, {"sqlite", "--x"}, {"sqlite", "/a", "/b"}, {"sqlite", "/a", "--since", "x"}} {
		var stderr bytes.Buffer
		if code := Run(append([]string{"restore"}, args...), strings.NewReader(""), io.Discard, &stderr); code != ExitUsage || !strings.Contains(stderr.String(), "usage:") {
			t.Errorf("%v: code %d, stderr %q", args, code, stderr.String())
		}
	}
}
