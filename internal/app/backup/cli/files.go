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

// dumpFiles walks the sources once (the fingerprint: every entry's name,
// size, mode and mtime), then writes a tar stream of them to stdout. Symlinks
// are stored as links, never followed; sockets, devices and FIFOs are
// skipped with a warning; files are read while the app runs.
func dumpFiles(sources []source, exclude []string, since string, stdout io.Writer) (Result, error) {
	var (
		res     Result
		entries []entry
		found   int
	)
	for _, s := range sources {
		got, warnings, err := walk(s, exclude)
		if errors.Is(err, fs.ErrNotExist) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: not found", displayName(s.entry)))
			continue
		}
		if err != nil {
			return Result{}, err
		}
		found++
		entries = append(entries, got...)
		res.Warnings = append(res.Warnings, warnings...)
	}
	if found == 0 {
		return res, fmt.Errorf("none of the backup's paths exist: %w", errMissing)
	}
	res.Fingerprint = filesFingerprint(entries)
	if since != "" && res.Fingerprint == since {
		return Result{}, errUnchanged
	}
	cw := &countWriter{w: stdout}
	tw := tar.NewWriter(cw)
	for _, e := range entries {
		warning, err := writeEntry(tw, e)
		if err != nil {
			return Result{}, err
		}
		if warning != "" {
			res.Warnings = append(res.Warnings, warning)
		}
	}
	if err := tw.Close(); err != nil {
		return Result{}, fmt.Errorf("write the archive: %w", err)
	}
	res.RawSize = cw.n
	return res, nil
}

func displayName(entry string) string {
	if entry == "" {
		return "the volume"
	}
	return "/" + entry
}

// walk lists a source: the path itself (unless it is the archive root) and,
// for a folder, everything below it in lexical order. Excluded entries are
// left out (a folder with its contents); special files become warnings.
func walk(s source, exclude []string) ([]entry, []string, error) {
	if _, err := os.Lstat(s.path); err != nil {
		return nil, nil, err
	}
	var (
		out      []entry
		warnings []string
	)
	err := filepath.WalkDir(s.path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p != s.path {
				return nil // removed meanwhile
			}
			if p != s.path {
				warnings = append(warnings, fmt.Sprintf("%s: %v", p, err))
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
			warnings = append(warnings, fmt.Sprintf("/%s: skipped (%s)", name, kindOf(m)))
			return nil
		}
		out = append(out, e)
		return nil
	})
	return out, warnings, err
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

// filesFingerprint hashes every entry's name, size, mode and mtime.
func filesFingerprint(entries []entry) string {
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%d\x00%o\x00%d\x00%s\n", e.name, e.info.Size(), uint32(e.info.Mode()), e.info.ModTime().UnixNano(), e.link)
	}
	return "files:" + hex.EncodeToString(h.Sum(nil)[:16])
}

// writeEntry archives one entry with its owner, mode and mtime. A file that
// vanished is skipped, one that shrank is padded, both with a warning.
func writeEntry(tw *tar.Writer, e entry) (string, error) {
	var f *os.File
	if e.info.Mode().IsRegular() {
		var err error
		if f, err = os.Open(e.path); err != nil {
			return fmt.Sprintf("/%s: skipped: %v", e.name, err), nil
		}
		defer f.Close()
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
