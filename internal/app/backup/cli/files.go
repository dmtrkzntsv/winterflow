package cli

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

// source is one --path: an absolute path in the helper (a mount, /src/<n>,
// or below it) and the name it has in the archive ("" = the archive root,
// for a whole volume).
type source struct {
	path  string
	entry string
}

// entry is one file system object to archive.
type entry struct {
	name string // archive name, "/"-separated, no leading "/"
	path string // where it is in the helper
	info fs.FileInfo
	link string // a symlink's target
}

// warner collects a Result's warnings, keeping the first maxWarnings and
// counting the rest, so a tree full of special files costs no memory.
type warner struct {
	list  []string
	extra int
}

func (w *warner) add(msg string) {
	if len(w.list) < maxWarnings {
		w.list = append(w.list, msg)
		return
	}
	w.extra++
}

func (w *warner) result() []string {
	if w.extra == 0 {
		return w.list
	}
	return append(w.list, fmt.Sprintf("and %d more", w.extra))
}

// dumpFiles walks the sources twice, so memory stays proportional to the
// tree's depth, not to its number of files. The first, streaming walk hashes
// every entry's name, size, mode and mtime into the fingerprint (and exits
// when it equals since); the second writes a tar stream to stdout. Symlinks
// are stored as links, never followed; sockets, devices and FIFOs are
// skipped with a warning; files are read while the app runs.
func dumpFiles(sources []source, exclude []string, since string, stdout io.Writer) (Result, error) {
	var (
		res   Result
		warns warner
		found int
	)
	h := sha256.New()
	for _, s := range sources {
		err := walk(s, exclude, warns.add, func(e entry) error {
			fmt.Fprintf(h, "%s\x00%d\x00%o\x00%d\x00%s\n", e.name, e.info.Size(), uint32(e.info.Mode()), e.info.ModTime().UnixNano(), e.link)
			return nil
		})
		if errors.Is(err, fs.ErrNotExist) {
			warns.add(fmt.Sprintf("%s: not found", displayName(s.entry)))
			continue
		}
		if err != nil {
			return Result{}, err
		}
		found++
	}
	if found == 0 {
		res.Warnings = warns.result()
		return res, fmt.Errorf("none of the backup's paths exist: %w", errMissing)
	}
	res.Fingerprint = "files:" + hex.EncodeToString(h.Sum(nil)[:16])
	if since != "" && res.Fingerprint == since {
		return Result{Warnings: warns.result()}, errUnchanged
	}
	cw := &countWriter{w: stdout}
	tw := tar.NewWriter(cw)
	for _, s := range sources {
		// The first walk already reported what it found; this one only
		// archives (a source that vanished since is skipped).
		err := walk(s, exclude, func(string) {}, func(e entry) error {
			warning, err := writeEntry(tw, e)
			if warning != "" {
				warns.add(warning)
			}
			return err
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Result{}, err
		}
	}
	if err := tw.Close(); err != nil {
		return Result{}, fmt.Errorf("write the archive: %w", err)
	}
	res.RawSize = cw.n
	res.Warnings = warns.result()
	return res, nil
}

func displayName(entry string) string {
	if entry == "" {
		return "the volume"
	}
	return "/" + entry
}

// walk streams a source to fn: the path itself (unless it is the archive
// root) and, for a folder, everything below it in lexical order. Excluded
// entries are left out (a folder with its contents); special files and
// unreadable folders are reported to warn. An error from fn stops the walk.
func walk(s source, exclude []string, warn func(string), fn func(entry) error) error {
	if _, err := os.Lstat(s.path); err != nil {
		return err
	}
	return filepath.WalkDir(s.path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p != s.path {
				return nil // removed meanwhile
			}
			if p != s.path {
				warn(fmt.Sprintf("%s: %v", p, err))
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			return err
		}
		rel, _ := filepath.Rel(s.path, p)
		name := s.entry
		if rel != "." {
			name = path.Join(s.entry, filepath.ToSlash(rel))
		}
		if name == "" { // the root of a whole volume
			return nil
		}
		if excluded(name, d.IsDir(), exclude) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // removed meanwhile
		}
		e := entry{name: name, path: p, info: info}
		switch m := info.Mode(); {
		case m.IsRegular(), m.IsDir():
		case m&fs.ModeSymlink != 0:
			if e.link, err = os.Readlink(p); err != nil {
				return nil
			}
		default:
			warn(fmt.Sprintf("/%s: skipped (%s)", name, kindOf(m)))
			return nil
		}
		return fn(e)
	})
}

func kindOf(m fs.FileMode) string {
	switch {
	case m&fs.ModeSocket != 0:
		return "a socket"
	case m&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case m&fs.ModeDevice != 0, m&fs.ModeCharDevice != 0:
		return "a device"
	}
	return "not a file"
}

// excluded matches an archive name against the exclude patterns
// (path.Match syntax). A leading "/" is ignored (names have none); a
// pattern ending in "/" matches folders only; a pattern without "/" matches
// the last name element, one with "/" the whole name.
func excluded(name string, dir bool, patterns []string) bool {
	for _, p := range patterns {
		dirOnly := strings.HasSuffix(p, "/")
		p = strings.Trim(p, "/")
		if p == "" || (dirOnly && !dir) {
			continue
		}
		target := name
		if !strings.Contains(p, "/") {
			target = path.Base(name)
		}
		if ok, _ := path.Match(p, target); ok {
			return true
		}
	}
	return false
}

// writeEntry archives one entry with its owner, mode and mtime, copying
// exactly the size recorded in its header. A file that vanished or is no
// longer a regular file is skipped, one that shrank is padded so the archive
// stays readable, both with a warning.
func writeEntry(tw *tar.Writer, e entry) (string, error) {
	var f *os.File
	if e.info.Mode().IsRegular() {
		var err error
		// The entry was a regular file when walked; it may have been swapped
		// for a FIFO (open would block) or a symlink (open would follow it)
		// since, so refuse both and check what was really opened.
		if f, err = os.OpenFile(e.path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0); err != nil {
			return fmt.Sprintf("/%s: skipped: %v", e.name, err), nil
		}
		defer f.Close()
		if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
			return fmt.Sprintf("/%s: skipped (changed while it was archived)", e.name), nil
		}
	}
	hdr, err := tar.FileInfoHeader(e.info, e.link)
	if err != nil {
		return fmt.Sprintf("/%s: skipped: %v", e.name, err), nil
	}
	hdr.Name = e.name
	if e.info.IsDir() {
		hdr.Name += "/"
	}
	hdr.Uname, hdr.Gname = "", ""
	hdr.Format = tar.FormatPAX
	if err := tw.WriteHeader(hdr); err != nil {
		return "", fmt.Errorf("write the archive: %w", err)
	}
	if f == nil {
		return "", nil
	}
	n, err := io.CopyN(tw, f, hdr.Size)
	if err == nil {
		return "", nil
	}
	if !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("archive /%s: %w", e.name, err)
	}
	if _, err := io.CopyN(tw, zeros{}, hdr.Size-n); err != nil {
		return "", fmt.Errorf("write the archive: %w", err)
	}
	return fmt.Sprintf("/%s: changed while it was archived", e.name), nil
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
