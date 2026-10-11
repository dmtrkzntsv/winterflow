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

// lchown changes an owner without following a symlink (replaced in tests,
// which can't give files away).
var lchown = os.Lchown

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
// file it replaces (of the folder, when there is none), the old -wal, -shm
// and -journal files are removed (SQLite would otherwise replay them onto
// the new file), and a rename puts it in place. Any failure before the
// rename leaves the original untouched. entry is the database's name in the
// backup, which messages use instead of the helper's /src/<n> path.
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
	n, err := writeNew(tmp, in)
	if err != nil {
		return Result{}, err
	}
	if n == 0 {
		return Result{}, errors.New("the copy is empty")
	}
	// immutable: SQLite opens it without locks and never creates a -wal or
	// -shm beside it (as root).
	if err := checkSnapshot(tmp, "immutable=1"); err != nil {
		return Result{}, fmt.Errorf("the copy can't be restored: %w", err)
	}
	if st, ok := owner.Sys().(*syscall.Stat_t); ok {
		if err := lchown(tmp, int(st.Uid), int(st.Gid)); err != nil {
			return Result{}, fmt.Errorf("give the copy its owner: %w", scrub(err))
		}
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return Result{}, scrub(err)
	}
	for _, ext := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(path + ext); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Result{}, fmt.Errorf("remove the old %s file: %w", ext, scrub(err))
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		return Result{}, fmt.Errorf("put the copy in place: %w", scrub(err))
	}
	syncDir(filepath.Dir(path))
	return Result{RawSize: n}, nil
}

// removeSQLiteFiles removes a database file and the files SQLite keeps
// beside it.
func removeSQLiteFiles(path string) {
	for _, ext := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(path + ext)
	}
}

// writeNew creates path (it must not exist; a symlink there is refused)
// with in's bytes, synced to disk.
func writeNew(path string, in io.Reader) (int64, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return 0, fmt.Errorf("write the copy: %w", scrub(err))
	}
	n, err := io.Copy(f, in)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, fmt.Errorf("write the copy: %w", scrub(err))
	}
	return n, nil
}

// syncDir makes a rename in dir durable (best effort).
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
