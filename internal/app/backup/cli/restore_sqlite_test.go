package cli

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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
	record := func(p string, uid, gid int) error {
		log.mu.Lock()
		log.calls[filepath.Base(p)] = [2]int{uid, gid}
		log.mu.Unlock()
		return nil
	}
	prevL, prevF := lchown, fchown
	lchown = record
	fchown = func(f *os.File, uid, gid int) error { return record(f.Name(), uid, gid) }
	t.Cleanup(func() { lchown, fchown = prevL, prevF })
	return log
}

func (l *chownLog) of(name string) ([2]int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.calls[name]
	return c, ok
}

// walFixture leaves a database at path whose rows (first+more) live partly in
// its -wal (never checkpointed, as after a killed app) and returns a dump
// taken when it had first rows.
func walFixture(t *testing.T, path string, first, more int) []byte {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	insert := func(n int, body string) {
		for range n {
			if _, err := db.Exec("INSERT INTO items (body) VALUES (?)", body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, body TEXT)"); err != nil {
		t.Fatal(err)
	}
	insert(first, "x")
	code, copied, res := run(t, "dump", "sqlite", path)
	if code != ExitOK {
		t.Fatalf("dump: %d %+v", code, res)
	}
	insert(more, "y")
	// Closing would checkpoint and delete the -wal: read both files first.
	main, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wal, err := os.ReadFile(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	for _, ext := range []string{"-wal", "-shm"} {
		_ = os.Remove(path + ext)
	}
	if err := os.WriteFile(path, main, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", wal, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	return copied
}

// A copy dumped while the app writes restores over a newer database that
// still has its (real, uncheckpointed) -wal: the rows are the copy's, the
// old -wal and -shm are gone (SQLite would replay them onto the new file),
// and the file keeps the owner and mode of the one it replaced.
func TestRestoreSQLite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	copied := walFixture(t, path, 50, 25)
	if err := os.WriteFile(path+"-shm", []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	chowns := recordChowns(t)
	// What the check left beside the copy is seen before the cleanup.
	t.Cleanup(func() { beforeSwap = nil })
	beforeSwap = func() {
		for _, ext := range []string{"-wal", "-shm", "-journal"} {
			if _, err := os.Stat(path + ".restore-tmp" + ext); !os.IsNotExist(err) {
				t.Errorf("checking the copy left %s", ext)
			}
		}
	}
	code, res := restore(t, copied, "sqlite", path)
	if code != ExitOK || res.RawSize != int64(len(copied)) {
		t.Fatalf("restore: %d %+v", code, res)
	}
	for _, ext := range []string{"-wal", "-shm", "-journal", ".restore-tmp", ".restore-old", "-wal.restore-old", "-shm.restore-old"} {
		if _, err := os.Stat(path + ext); !os.IsNotExist(err) {
			t.Fatalf("%s left behind", ext)
		}
	}
	if n := count(t, path); n != 50 {
		t.Fatalf("restored database has %d rows, want 50", n)
	}
	var check string
	if err := openDB(t, path).QueryRow("PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		t.Fatalf("quick_check = %q, %v", check, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640", info.Mode())
	}
	if c, ok := chowns.of("app.db.restore-tmp"); !ok || c != [2]int{os.Getuid(), os.Getgid()} {
		t.Fatalf("owner given = %v (%v)", c, ok)
	}
}

// A swap that fails puts the old -wal back: the original still reads every
// row, including those only its -wal holds, and nothing is left aside.
func TestRestoreSQLiteFailedSwapKeepsTheWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	copied := walFixture(t, path, 50, 25)
	if err := os.WriteFile(path+"-journal", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	mainBefore, _ := os.ReadFile(path)
	walBefore, _ := os.ReadFile(path + "-wal")
	recordChowns(t)
	prev := rename
	t.Cleanup(func() { rename = prev })
	rename = func(from, to string) error {
		if to == path {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EPERM}
		}
		return prev(from, to)
	}
	code, res := restore(t, copied, "sqlite", path)
	if code != ExitFailed || !strings.HasPrefix(res.Error, "put the copy in place: ") || strings.Contains(res.Error, dir) {
		t.Fatalf("%d %+v", code, res)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, mainBefore) {
		t.Fatal("the database changed")
	}
	if now, _ := os.ReadFile(path + "-wal"); !bytes.Equal(now, walBefore) {
		t.Fatal("the -wal did not come back")
	}
	if _, err := os.Stat(path + "-journal"); err != nil {
		t.Fatalf("the -journal did not come back: %v", err)
	}
	for _, ext := range []string{"-wal.restore-old", "-journal.restore-old", ".restore-tmp"} {
		if _, err := os.Stat(path + ext); !os.IsNotExist(err) {
			t.Fatalf("%s left behind", ext)
		}
	}
	if err := os.Remove(path + "-journal"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, path); n != 75 {
		t.Fatalf("the original reads %d rows, want 75", n)
	}
}

// When the old files can't be put back either, the restore is partial (exit
// 5): they stay set aside, and the caller must not treat the data as intact.
func TestRestoreSQLitePutBackFailureIsPartial(t *testing.T) {
	for name, fails := range map[string]func(to string) bool{
		// the swap fails, and so does every put-back
		"swap": func(to string) bool { return !strings.HasSuffix(to, restoreOld) },
		// setting the -journal aside fails, and so does putting the -wal back
		"set aside": func(to string) bool {
			return strings.HasSuffix(to, "-journal"+restoreOld) || strings.HasSuffix(to, "app.db-wal")
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "app.db")
			copied := walFixture(t, path, 50, 25)
			if err := os.WriteFile(path+"-journal", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			recordChowns(t)
			prev := rename
			t.Cleanup(func() { rename = prev })
			rename = func(from, to string) error {
				if fails(to) {
					return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EIO}
				}
				return prev(from, to)
			}
			code, res := restore(t, copied, "sqlite", path)
			if code != ExitPartial || !strings.Contains(res.Error, "stay set aside as *"+restoreOld) || strings.Contains(res.Error, "partial") || strings.Contains(res.Error, dir) {
				t.Fatalf("%d %+v", code, res)
			}
			if _, err := os.Stat(path + "-wal" + restoreOld); err != nil {
				t.Fatalf("the old -wal is not kept aside: %v", err)
			}
		})
	}
}

// The owner and mode are set through the open file, not the copy's path: a
// symlink put there as soon as the copy is written gets neither.
func TestRestoreSQLiteGivesOwnerAndModeByDescriptor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	copied := walFixture(t, path, 10, 5)
	target := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	chowns := recordChowns(t)
	t.Cleanup(func() { afterWrite = nil })
	afterWrite = func() {
		tmp := path + ".restore-tmp"
		if err := os.Remove(tmp); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(target, tmp); err != nil {
			t.Error(err)
		}
	}
	if code, res := restore(t, copied, "sqlite", path); code != ExitFailed {
		t.Fatalf("%d %+v", code, res)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o600 {
		t.Fatalf("the target's mode became %v", info.Mode())
	}
	if _, ok := chowns.of("secret"); ok {
		t.Fatal("the target was chowned")
	}
}

// A leftover .restore-old of a killed run is replaced by the live file and
// removed after a successful restore.
func TestRestoreSQLiteClearsLeftoverAsides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	copied := walFixture(t, path, 10, 5)
	for _, ext := range []string{"-wal.restore-old", "-shm.restore-old", "-journal.restore-old"} {
		if err := os.WriteFile(path+ext, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	recordChowns(t)
	if code, res := restore(t, copied, "sqlite", path); code != ExitOK {
		t.Fatalf("%d %+v", code, res)
	}
	if left, _ := filepath.Glob(path + "*restore-old"); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	if n := count(t, path); n != 10 {
		t.Fatalf("%d rows", n)
	}
}

// The temp file swapped for a symlink after it was written is never chmodded,
// chowned or moved into place: the target keeps its mode, the database is
// untouched.
func TestRestoreSQLiteRefusesASwappedCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	copied := walFixture(t, path, 10, 5)
	mainBefore, _ := os.ReadFile(path)
	walBefore, _ := os.ReadFile(path + "-wal")
	target := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	chowns := recordChowns(t)
	t.Cleanup(func() { beforeSwap = nil })
	beforeSwap = func() {
		tmp := path + ".restore-tmp"
		if err := os.Remove(tmp); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(target, tmp); err != nil {
			t.Error(err)
		}
	}
	code, res := restore(t, copied, "sqlite", path)
	if code != ExitFailed || !strings.HasPrefix(res.Error, "the copy changed while it was checked") {
		t.Fatalf("%d %+v", code, res)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o600 {
		t.Fatalf("the target's mode became %v", info.Mode())
	}
	if _, ok := chowns.of("secret"); ok {
		t.Fatal("the target was chowned")
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, mainBefore) {
		t.Fatal("the database changed")
	}
	if now, _ := os.ReadFile(path + "-wal"); !bytes.Equal(now, walBefore) {
		t.Fatal("the -wal changed")
	}
	if _, err := os.Lstat(path + ".restore-tmp"); !os.IsNotExist(err) {
		t.Fatal("the swapped-in file was left")
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
