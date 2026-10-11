package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// lchown changes an owner without following a symlink; fchown changes the
// owner of an open file (both replaced in tests, which can't give files
// away).
var (
	lchown = os.Lchown
	fchown = func(f *os.File, uid, gid int) error { return f.Chown(uid, gid) }
)

func restoreSQLiteCmd(args []string, stdin io.Reader) (Result, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return Result{}, fmt.Errorf("%w: the database path comes first", errUsage)
	}
	fs := flag.NewFlagSet("restore sqlite", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	entry := fs.String("entry", "", "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		return Result{}, fmt.Errorf("%w: restore sqlite takes <path> [--entry <name>]", errUsage)
	}
	return restoreSQLite(args[0], strings.Trim(*entry, "/"), stdin)
}

// restoreSQLite replaces the database at path with the copy read from in.
// The copy is written beside it as <path>.restore-tmp and must pass
// quick_check with a non-empty schema; it takes the owner and mode of the
// file it replaces (of the folder, when there is none), through the open
// descriptor, so a file swapped in at its path is never touched. The old
// -wal, -shm and -journal files are moved aside as <file>.restore-old (SQLite
// would otherwise replay them onto the new file), a rename puts the copy in
// place, and the moved files are removed only after it. Any failure puts them
// back: the original, with its WAL, is as it was. When putting them back
// fails too, the restore is errPartial (exit 5: they stay aside). (A helper
// killed in between leaves them aside, not lost.) entry is the database's name in the backup,
// which messages use instead of the helper's /src/<n> path.
func restoreSQLite(path, entry string, in io.Reader) (Result, error) {
	shown := path
	if entry != "" {
		shown = "/" + entry
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return Result{}, fmt.Errorf("the folder of %s: %w", shown, scrub(err))
	}
	if !dir.IsDir() {
		return Result{}, fmt.Errorf("the folder of %s is not a folder", shown)
	}
	owner, perm := dir, dir.Mode().Perm()&0o666
	switch info, err := os.Lstat(path); {
	case err == nil && !info.Mode().IsRegular():
		return Result{}, fmt.Errorf("%s is not a file", shown)
	case err == nil:
		owner, perm = info, info.Mode().Perm()
	case !errors.Is(err, fs.ErrNotExist):
		return Result{}, fmt.Errorf("%s: %w", shown, scrub(err))
	}
	tmp := path + ".restore-tmp"
	removeSQLiteFiles(tmp)
	defer removeSQLiteFiles(tmp)
	f, n, err := writeNew(tmp, in)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	if n == 0 {
		return Result{}, errors.New("the copy is empty")
	}
	if afterWrite != nil {
		afterWrite()
	}
	// The owner and mode go on through the descriptor (fchown, fchmod):
	// they never follow whatever has since appeared at the path.
	if st, ok := owner.Sys().(*syscall.Stat_t); ok {
		if err := fchown(f, int(st.Uid), int(st.Gid)); err != nil {
			return Result{}, fmt.Errorf("give the copy its owner: %w", scrub(err))
		}
	}
	if err := f.Chmod(perm); err != nil {
		return Result{}, fmt.Errorf("give the copy its mode: %w", scrub(err))
	}
	if err := f.Sync(); err != nil {
		return Result{}, fmt.Errorf("write the copy: %w", scrub(err))
	}
	// immutable: SQLite opens it without locks and never creates a -wal or
	// -shm beside it (as root).
	if err := checkSnapshot(tmp, "immutable=1"); err != nil {
		return Result{}, fmt.Errorf("the copy can't be restored: %w", err)
	}
	if beforeSwap != nil {
		beforeSwap()
	}
	// What is at the path must still be the file written and checked.
	if err := sameFile(f, tmp); err != nil {
		return Result{}, fmt.Errorf("the copy changed while it was checked: %w", err)
	}
	asides, err := moveAside(path)
	if err != nil {
		return Result{}, err
	}
	if err := rename(tmp, path); err != nil {
		err = fmt.Errorf("put the copy in place: %w", scrub(err))
		if rerr := putBack(path, asides); rerr != nil {
			err = fmt.Errorf("%w; %v: %w", err, rerr, errPartial)
		}
		return Result{}, err
	}
	for _, ext := range restoreOldExts {
		_ = os.Remove(path + ext + restoreOld) // also the leftovers of a killed run
	}
	syncDir(filepath.Dir(path))
	return Result{RawSize: n}, nil
}

// restoreOld is the suffix of an old -wal, -shm or -journal file set aside
// while a restore swaps the database. A leftover of a killed run is never
// read by SQLite and goes with the next successful restore.
const restoreOld = ".restore-old"

var restoreOldExts = []string{"-wal", "-shm", "-journal"}

// Test seams: afterWrite runs once the copy is written, beforeSwap between
// its check and the swap; rename is os.Rename.
var (
	afterWrite func()
	beforeSwap func()
	rename     = os.Rename
)

// moveAside renames each existing <path>-wal, -shm and -journal to
// <file>.restore-old (replacing a leftover one) and returns the extensions it
// moved. On failure it puts back what it had moved.
func moveAside(path string) ([]string, error) {
	var moved []string
	for _, ext := range restoreOldExts {
		err := rename(path+ext, path+ext+restoreOld)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			err = fmt.Errorf("set the old %s file aside: %w", ext, scrub(err))
			if rerr := putBack(path, moved); rerr != nil {
				err = fmt.Errorf("%w; %v: %w", err, rerr, errPartial)
			}
			return nil, err
		}
		moved = append(moved, ext)
	}
	return moved, nil
}

// putBack undoes moveAside for the extensions in moved.
func putBack(path string, moved []string) error {
	var failed []string
	for _, ext := range moved {
		if err := rename(path+ext+restoreOld, path+ext); err != nil {
			failed = append(failed, ext)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("the old %s files stay set aside as *%s", strings.Join(failed, ", "), restoreOld)
	}
	return nil
}

// sameFile requires path to be the regular file f is open on.
func sameFile(f *os.File, path string) error {
	want, err := f.Stat()
	if err != nil {
		return scrub(err)
	}
	got, err := os.Lstat(path)
	if err != nil {
		return scrub(err)
	}
	if !got.Mode().IsRegular() || !os.SameFile(want, got) {
		return errors.New("it was replaced")
	}
	return nil
}

// removeSQLiteFiles removes a database file and the files SQLite keeps
// beside it.
func removeSQLiteFiles(path string) {
	for _, ext := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(path + ext)
	}
}

// writeNew creates path (it must not exist; a symlink there is refused)
// with in's bytes and returns the file, still open (the caller closes it and
// syncs it once it has its owner and mode).
func writeNew(path string, in io.Reader) (*os.File, int64, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, 0, fmt.Errorf("write the copy: %w", scrub(err))
	}
	n, err := io.Copy(f, in)
	if err != nil {
		f.Close()
		return nil, n, fmt.Errorf("write the copy: %w", scrub(err))
	}
	return f, n, nil
}

// syncDir makes a rename in dir durable (best effort).
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
