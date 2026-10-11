package cli

import (
	"archive/tar"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// errPartial is a files restore that failed while swapping (exit 5).
var errPartial = errors.New("partial")

// restoreTmpPrefix starts the temp folder a files restore extracts into, at
// the root of each mount: .wf-restore-<id>.
const restoreTmpPrefix = ".wf-restore-"

// freeSpace is the room left on the file system holding path (replaced in
// tests).
var freeSpace = func(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

var restoreIDRE = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// target is one path of a files restore: src in the helper, below (or at)
// the mount root, holding the archive's entries named entry ("" = the
// archive root, a whole volume).
type target struct {
	root  string
	src   string
	sub   string // src relative to root, "" = the mount itself
	entry string
}

func (t target) display() string { return displayName(t.entry) }

func restoreFilesCmd(args []string, stdin io.Reader) (Result, error) {
	fs := flag.NewFlagSet("restore files", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var roots, srcs, entries, needs list
	fs.Var(&roots, "root", "")
	fs.Var(&srcs, "src", "")
	fs.Var(&entries, "entry", "")
	fs.Var(&needs, "need", "")
	id := fs.String("id", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || len(srcs) == 0 || len(srcs) != len(entries) || len(srcs) != len(roots) {
		return Result{}, fmt.Errorf("%w: restore files takes --root <mount> --src <source> --entry <entry name> triples", errUsage)
	}
	if !restoreIDRE.MatchString(*id) {
		return Result{}, fmt.Errorf("%w: restore files needs --id (lowercase letters, digits, hyphens)", errUsage)
	}
	targets := make([]target, 0, len(srcs))
	for i, src := range srcs {
		root, src := filepath.Clean(roots[i]), filepath.Clean(src)
		sub, err := filepath.Rel(root, src)
		if !filepath.IsAbs(root) || err != nil || sub == ".." || strings.HasPrefix(sub, "../") {
			return Result{}, fmt.Errorf("%w: --src %q is not below its --root %q", errUsage, srcs[i], roots[i])
		}
		if sub == "." {
			sub = ""
		}
		targets = append(targets, target{root: root, src: src, sub: filepath.ToSlash(sub), entry: strings.Trim(entries[i], "/")})
	}
	need := map[string]uint64{}
	for _, n := range needs {
		i := strings.LastIndex(n, "=")
		bytes, err := strconv.ParseUint(n[i+1:], 10, 64)
		if i <= 0 || err != nil {
			return Result{}, fmt.Errorf("%w: --need takes <mount>=<bytes>", errUsage)
		}
		need[filepath.Clean(n[:i])] += bytes
	}
	return restoreFiles(targets, *id, need, stdin)
}

// restoreFiles replaces the targets' paths with the archive read from in.
// Everything is first extracted into .wf-restore-<id>/ at the root of each
// mount (after checking each mount has room for its share, need), refusing
// names that are absolute, hold "..", lie outside the targets, or would be
// written through a symlink; a failure there removes the temp folders and
// leaves every mount untouched. Then each path is swapped: what is there is
// removed and the restored entry moved in (for a whole mount, every entry
// at its root but the temp folder). A failure while swapping is errPartial.
// Paths the archive doesn't hold are left as they are, with a warning.
func restoreFiles(targets []target, id string, need map[string]uint64, in io.Reader) (Result, error) {
	var warns warner
	tmpName := restoreTmpPrefix + id
	roots := map[string]string{} // mount root -> its temp folder
	for _, t := range targets {
		roots[t.root] = filepath.Join(t.root, tmpName)
	}
	for _, t := range targets {
		if err := checkParents(t); err != nil {
			return Result{}, err
		}
	}
	for root := range roots {
		if err := checkRoom(root, need[root], targets); err != nil {
			return Result{}, err
		}
	}
	defer func() {
		for _, tmp := range roots {
			_ = os.RemoveAll(tmp)
		}
	}()
	for root, tmp := range roots {
		removeStale(root)
		if err := os.Mkdir(tmp, 0o700); err != nil {
			return Result{}, fmt.Errorf("make the restore folder: %w", scrub(err))
		}
	}
	x := extraction{targets: targets, roots: roots, warn: warns.add, seen: map[int]bool{}, rootHeaders: map[string]*tar.Header{}}
	n, err := x.extract(in)
	if err != nil {
		return Result{}, err
	}
	for i, t := range targets {
		if !x.seen[i] {
			warns.add(t.display() + ": not in the copy, left as it is")
		}
	}
	if err := swap(targets, roots, x.seen); err != nil {
		return Result{Warnings: warns.result()}, fmt.Errorf("%w: %w", err, errPartial)
	}
	for root, h := range x.rootHeaders {
		if err := applyMeta(root, h); err != nil {
			warns.add(fmt.Sprintf("/%s: owner, mode or times not restored: %v", name(h), scrub(err)))
		}
	}
	return Result{RawSize: n, Warnings: warns.result()}, nil
}

// checkParents refuses a path whose folders below the mount root are not
// real folders: a symlink there would move the restore elsewhere.
func checkParents(t target) error {
	if t.sub == "" {
		return nil
	}
	p := t.root
	parts := strings.Split(t.sub, "/")
	for _, part := range parts[:len(parts)-1] {
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: %w", t.display(), scrub(err))
		}
		if !info.IsDir() {
			return fmt.Errorf("refused: a folder above %s is not a folder (a symlink?), so it can't be restored there", t.display())
		}
	}
	return nil
}

// checkRoom requires the mount at root to have room for the restored copy
// next to the current data.
func checkRoom(root string, need uint64, targets []target) error {
	if need == 0 {
		return nil
	}
	free, err := freeSpace(root)
	if err != nil {
		return fmt.Errorf("check the free space for the restore: %w", scrub(err))
	}
	if free < need {
		name := root
		for _, t := range targets {
			if t.root == root {
				name = t.display()
				break
			}
		}
		return fmt.Errorf("not enough free space to restore %s: it needs %s next to the current data, %s is free", name, human(need), human(free))
	}
	return nil
}

func human(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// removeStale removes the temp folders a killed restore left at root.
func removeStale(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), restoreTmpPrefix) {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// extraction is one archive being extracted into the temp folders.
type extraction struct {
	targets     []target
	roots       map[string]string
	warn        func(string)
	seen        map[int]bool           // targets the archive holds entries of
	rootHeaders map[string]*tar.Header // a mount root's own entry, applied after the swap
	dirs        []extractedDir         // folders, their metadata applied last
}

type extractedDir struct {
	path string
	hdr  *tar.Header
}

// extract reads the archive into the temp folders; it returns the archive's
// size.
func (x *extraction) extract(in io.Reader) (int64, error) {
	cr := &countReader{r: in}
	tr := tar.NewReader(cr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("read the archive: %w", err)
		}
		if err := x.entry(h, tr); err != nil {
			return 0, err
		}
	}
	// Deepest first: a folder's mtime changes while its children are made,
	// and a read-only folder must stay writable until they are.
	for i := len(x.dirs) - 1; i >= 0; i-- {
		if err := applyMeta(x.dirs[i].path, x.dirs[i].hdr); err != nil {
			return 0, fmt.Errorf("restore /%s: %w", name(x.dirs[i].hdr), scrub(err))
		}
	}
	return cr.n, nil
}

// match is the target holding an archive name: the one with the longest
// entry that is the name or a folder above it. rel is the name below that
// entry.
func (x *extraction) match(name string, proper bool) (int, string, bool) {
	best, rel := -1, ""
	for i, t := range x.targets {
		var r string
		switch {
		case t.entry == "":
			r = name
		case name == t.entry && !proper:
		case strings.HasPrefix(name, t.entry+"/"):
			r = strings.TrimPrefix(name, t.entry+"/")
		default:
			continue
		}
		if best < 0 || len(t.entry) > len(x.targets[best].entry) {
			best, rel = i, r
		}
	}
	return best, rel, best >= 0
}

// entry extracts one archive entry.
func (x *extraction) entry(h *tar.Header, r io.Reader) error {
	name := strings.TrimSuffix(h.Name, "/")
	if name == "" || strings.HasPrefix(name, "/") || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
		return fmt.Errorf("refused: the archive entry %q is not a relative path", h.Name)
	}
	i, rel, ok := x.match(name, false)
	if !ok {
		return fmt.Errorf("refused: the archive entry /%s is outside the backup's paths", name)
	}
	x.seen[i] = true
	t := x.targets[i]
	if err := x.place(t, rel, h, r); err != nil {
		return err
	}
	// A nested mount's own folder is also an (empty) folder of the mount
	// above it: the dump archives it twice, once from each. Keep the outer
	// copy too, so the mount point is there after the swap.
	if h.Typeflag == tar.TypeDir && rel == "" && t.sub == "" {
		if j, outerRel, ok := x.match(name, true); ok && x.targets[j].root != t.root {
			return x.place(x.targets[j], outerRel, h, nil)
		}
	}
	return nil
}

// place writes entry h (rel below target t) into t's temp folder.
func (x *extraction) place(t target, rel string, h *tar.Header, r io.Reader) error {
	dest := path.Join(t.sub, rel)
	if dest == "" || dest == "." {
		if h.Typeflag == tar.TypeDir {
			x.rootHeaders[t.root] = h
		}
		return nil
	}
	if strings.HasPrefix(strings.SplitN(dest, "/", 2)[0], restoreTmpPrefix) {
		x.warn(fmt.Sprintf("/%s: skipped (a restore's temp folder)", h.Name))
		return nil
	}
	tmp := x.roots[t.root]
	full, err := makeParents(tmp, dest)
	if err != nil {
		return err
	}
	switch h.Typeflag {
	case tar.TypeDir:
		info, err := os.Lstat(full)
		switch {
		case err == nil && info.IsDir():
		case err == nil:
			if err := os.Remove(full); err != nil {
				return err
			}
			fallthrough
		default:
			if err := os.Mkdir(full, 0o700); err != nil {
				return fmt.Errorf("restore /%s: %w", name(h), scrub(err))
			}
		}
		x.dirs = append(x.dirs, extractedDir{path: full, hdr: h})
	case tar.TypeReg:
		if r == nil {
			return nil
		}
		if info, err := os.Lstat(full); err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("refused: /%s would be written through a symlink or over a folder", name(h))
		}
		f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600)
		if err != nil {
			return fmt.Errorf("restore /%s: %w", name(h), scrub(err))
		}
		_, err = io.CopyN(f, r, h.Size)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("restore /%s: %w", name(h), scrub(err))
		}
		if err := applyMeta(full, h); err != nil {
			return fmt.Errorf("restore /%s: %w", name(h), scrub(err))
		}
	case tar.TypeSymlink:
		if info, err := os.Lstat(full); err == nil {
			if info.IsDir() {
				return fmt.Errorf("refused: /%s is a folder and a symlink in the archive", name(h))
			}
			if err := os.Remove(full); err != nil {
				return err
			}
		}
		if err := os.Symlink(h.Linkname, full); err != nil {
			return fmt.Errorf("restore /%s: %w", name(h), scrub(err))
		}
		if err := lchown(full, h.Uid, h.Gid); err != nil {
			return fmt.Errorf("restore /%s: %w", name(h), scrub(err))
		}
	default:
		x.warn(fmt.Sprintf("/%s: skipped (not a file, folder or symlink)", name(h)))
	}
	return nil
}

func name(h *tar.Header) string { return strings.TrimSuffix(h.Name, "/") }

// makeParents makes the folders above rel inside tmp, refusing a symlink on
// the way (it would lead the restore out of tmp), and returns rel's path.
func makeParents(tmp, rel string) (string, error) {
	p := tmp
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Mkdir(p, 0o700); err != nil {
				return "", err
			}
		case err != nil:
			return "", err
		case !info.IsDir():
			return "", fmt.Errorf("refused: /%s would be written through a symlink", rel)
		}
	}
	return filepath.Join(p, parts[len(parts)-1]), nil
}

// applyMeta gives a restored file or folder the archive's owner, mode and
// times (owner first: chown clears setuid bits).
func applyMeta(p string, h *tar.Header) error {
	if err := lchown(p, h.Uid, h.Gid); err != nil {
		return err
	}
	mode := h.FileInfo().Mode() & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
	if err := os.Chmod(p, mode); err != nil {
		return err
	}
	atime := h.AccessTime
	if atime.IsZero() {
		atime = h.ModTime
	}
	return os.Chtimes(p, atime, h.ModTime)
}

// swap moves each restored path into place, shortest first: a path inside
// another one moved with it.
func swap(targets []target, roots map[string]string, seen map[int]bool) error {
	order := make([]int, 0, len(targets))
	for i := range targets {
		if seen[i] {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return len(targets[order[a]].sub) < len(targets[order[b]].sub) })
	for _, i := range order {
		t := targets[i]
		tmp := roots[t.root]
		if t.sub == "" {
			if err := swapMount(t.root, tmp); err != nil {
				return fmt.Errorf("%s was partly replaced: %w", t.display(), scrub(err))
			}
			continue
		}
		src := filepath.Join(tmp, t.sub)
		if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
			continue // moved with a path above it
		}
		dst := filepath.Join(t.root, t.sub)
		if err := os.RemoveAll(dst); err != nil {
			return fmt.Errorf("%s was partly replaced: %w", t.display(), scrub(err))
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("%s was removed but not restored: %w", t.display(), scrub(err))
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("%s was removed but not restored: %w", t.display(), scrub(err))
		}
	}
	for root := range roots {
		syncDir(root)
	}
	return nil
}

// swapMount replaces everything at a mount's root (but restore temp
// folders) with tmp's entries.
func swapMount(root, tmp string) error {
	old, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range old {
		if strings.HasPrefix(e.Name(), restoreTmpPrefix) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			return err
		}
	}
	restored, err := os.ReadDir(tmp)
	if err != nil {
		return err
	}
	for _, e := range restored {
		if err := os.Rename(filepath.Join(tmp, e.Name()), filepath.Join(root, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
