// Package fsutil writes files so a power cut leaves the old content or the
// new, never a truncated file: each write goes to a temp file that is
// fsynced, renamed into place, and made durable by fsyncing the folder.
package fsutil

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WriteFile writes data (mode perm) to path through the temp file
// path+".tmp", removed again when a step fails.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err == nil {
		err = commit(f, path, func(f *os.File) error { _, err := f.Write(data); return err })
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// WriteReader streams r into path (mode 0600) through a temp file of its
// own, created in path's folder with os.CreateTemp(pattern): concurrent
// writes of one path never share a temp file, and the last rename wins
// whole. A reader that fails leaves path untouched.
func WriteReader(path, pattern string, r io.Reader) error {
	f, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return err
	}
	if err := commit(f, path, func(f *os.File) error { _, err := io.Copy(f, r); return err }); err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	return nil
}

// commit fills the open temp file f, fsyncs and closes it, renames it to
// path and fsyncs path's folder. The caller removes f on an error.
func commit(f *os.File, path string, fill func(*os.File) error) error {
	err := fill(f)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return err
	}
	SyncDir(filepath.Dir(path))
	return nil
}

// SyncDir fsyncs a folder so the renames and removals in it are durable.
// Best effort: some filesystems refuse to sync a folder, and the files
// themselves are already synced.
func SyncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
