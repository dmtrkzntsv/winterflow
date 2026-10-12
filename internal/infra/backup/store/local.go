package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"winterflow/pkg/fsutil"
)

// local stores objects as files under root: directories 0700, files 0600,
// each Put written atomically through its own temp file (tmpPrefix…, a name
// no key can have).
type local struct{ root string }

// openLocal requires root to be an existing folder: winterflow never creates
// it, so an unmounted disk fails here (for a folder on the disk) instead of
// filling the root filesystem.
func openLocal(root string) (*local, error) {
	l := &local{root: root}
	if err := l.checkRoot(); err != nil {
		return nil, err
	}
	return l, nil
}

// checkRoot requires the root to be an existing folder and, under a mount
// root, a folder on a mounted disk (checkMounted). Every call re-checks it,
// so writes fail once the folder itself is gone (a folder on a disk that was
// unplugged after Open) instead of landing elsewhere. A missing root matches
// fs.ErrNotExist.
func (l *local) checkRoot() error {
	info, err := os.Stat(l.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return missingFolderError(l.root)
		}
		return fmt.Errorf("cannot open %s: %w", l.root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a folder", l.root)
	}
	return checkMounted(l.root)
}

// MountRoots are the folders disks and network shares are mounted under: the
// systemd unit lets the agent write there (ReadWritePaths=-/mnt -/media).
var MountRoots = []string{"/mnt", "/media"}

// deviceOf is the id of the filesystem path lives on (st_dev); replaced in
// tests.
var deviceOf = func(path string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Dev), nil
}

// checkMounted refuses a folder under a mount root that is on the same
// filesystem as "/": the mount point of a disk that is not mounted (after a
// reboot, or unplugged) is an empty folder on the system disk, and backups
// written there would quietly fill it. Comparing with the folder's parent
// instead would refuse every folder inside a mounted disk (the recommended
// /mnt/usb/winterflow); comparing with "/" accepts those and still refuses a
// bare, unmounted /mnt/usb. Folders outside the mount roots are not checked:
// the operator allowed them with a systemd drop-in.
func checkMounted(folder string) error {
	for _, root := range MountRoots {
		if !inside(folder, root) {
			continue
		}
		sys, err := deviceOf("/")
		if err != nil {
			return nil // nothing to compare with
		}
		dev, err := deviceOf(folder)
		if err != nil {
			return fmt.Errorf("cannot open %s: %w", folder, err)
		}
		if dev == sys {
			return notMountedError(folder)
		}
		return nil
	}
	return nil
}

// inside reports whether path is dir or below it.
func inside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// notMountedError is a folder under /mnt or /media that is on the system
// disk: usually a disk that is not mounted.
type notMountedError string

func (e notMountedError) Error() string {
	return "the storage folder " + string(e) + " is on the system disk, not on a mounted disk or share (is the disk mounted?)"
}

// missingFolderError is a storage folder that does not exist: usually a disk
// that is not mounted.
type missingFolderError string

func (e missingFolderError) Error() string {
	return "the storage folder " + string(e) + " is missing (is the disk mounted?)"
}

func (missingFolderError) Unwrap() error { return fs.ErrNotExist }

// mkdirBelow creates the folders of rel (a clean, "/"-separated path) one
// level at a time under root. Unlike MkdirAll it can never recreate root
// itself: a root that vanished meanwhile fails the first Mkdir.
func (l *local) mkdirBelow(rel string) error {
	if rel == "." {
		return nil
	}
	dir := l.root
	for _, seg := range strings.Split(rel, "/") {
		dir = filepath.Join(dir, seg)
		err := os.Mkdir(dir, 0o700)
		switch {
		case err == nil:
		case errors.Is(err, fs.ErrExist):
			info, err := os.Stat(dir)
			if err != nil {
				return err
			}
			if !info.IsDir() {
				return fmt.Errorf("%s is not a folder", dir)
			}
		case errors.Is(err, fs.ErrNotExist):
			// The root (or a folder just made) vanished meanwhile.
			if rerr := l.checkRoot(); rerr != nil {
				return rerr
			}
			return err
		default:
			return err
		}
	}
	return nil
}

func (l *local) file(key string) string { return filepath.Join(l.root, filepath.FromSlash(key)) }

func (l *local) Put(ctx context.Context, key string, r io.Reader) error {
	key, err := cleanKey(key)
	if err != nil {
		return err
	}
	if err := l.checkRoot(); err != nil {
		return err
	}
	if err := l.mkdirBelow(path.Dir(key)); err != nil {
		return err
	}
	return fsutil.WriteReader(l.file(key), tmpPrefix+"*", CtxReader{ctx, r})
}

func (l *local) Get(_ context.Context, key string) (io.ReadCloser, error) {
	key, err := cleanKey(key)
	if err != nil {
		return nil, err
	}
	if err := l.checkRoot(); err != nil {
		return nil, err
	}
	return os.Open(l.file(key))
}

func (l *local) List(ctx context.Context, prefix string) ([]Object, error) {
	prefix, err := cleanPrefix(prefix)
	if err != nil {
		return nil, err
	}
	if err := l.checkRoot(); err != nil {
		return nil, err
	}
	// Walk only the folder the prefix lives in.
	start := l.root
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		start = l.file(prefix[:i])
	}
	var out []Object
	err = filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() || strings.HasPrefix(d.Name(), tmpPrefix) {
			return nil
		}
		rel, err := filepath.Rel(l.root, p)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // removed meanwhile
		}
		out = append(out, Object{Key: key, Size: info.Size(), ModTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Delete removes the files and then the folders left empty, up to root.
func (l *local) Delete(ctx context.Context, keys ...string) error {
	if err := l.checkRoot(); err != nil {
		return err
	}
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, err := cleanKey(key)
		if err != nil {
			return err
		}
		if err := os.Remove(l.file(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		for dir := path.Dir(key); dir != "."; dir = path.Dir(dir) {
			if os.Remove(l.file(dir)) != nil {
				break
			}
		}
	}
	return nil
}

// CtxReader stops a copy once Ctx is done.
type CtxReader struct {
	Ctx context.Context
	R   io.Reader
}

func (c CtxReader) Read(p []byte) (int, error) {
	if err := c.Ctx.Err(); err != nil {
		return 0, err
	}
	return c.R.Read(p)
}
