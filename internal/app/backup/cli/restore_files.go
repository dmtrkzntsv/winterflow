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
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"winterflow/internal/infra/backup/helper"
)

// errPartial is a files restore that failed while swapping and could not put
// things back (exit 5).
var errPartial = errors.New("partial")

// restoreTmpPrefix starts the temp folders a files restore makes at the root
// of each mount: .wf-restore-<id> (the extracted copy) and
// .wf-restore-<id>.old (the data it replaces, until the swap is done).
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

// devOf is the device a path is on (replaced in tests).
var devOf = func(p string) (uint64, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("no device for " + p)
	}
	return uint64(st.Dev), nil
}

// mountPoints lists where file systems are mounted in the helper (replaced
// in tests).
var mountPoints = func() ([]string, error) {
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return parseMountInfo(string(raw)), nil
}

// parseMountInfo returns the mount points of a /proc/<pid>/mountinfo (its
// fifth field, with \040-style escapes).
func parseMountInfo(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		out = append(out, unescapeMount(fields[4]))
	}
	return out
}

func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
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

func restoreFilesCmd(args []string, stdin io.Reader, stderr io.Writer) (Result, error) {
	fs := flag.NewFlagSet("restore files", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var roots, srcs, entries, needs, exclude list
	fs.Var(&roots, "root", "")
	fs.Var(&srcs, "src", "")
	fs.Var(&entries, "entry", "")
	fs.Var(&needs, "need", "")
	fs.Var(&exclude, "exclude", "")
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
	return restoreFilesNotify(targets, *id, need, exclude, stdin, func() { fmt.Fprintln(stderr, helper.SwappingLine) })
}

// restoreFiles replaces the targets' paths with the archive read from in.
// Everything is first extracted into .wf-restore-<id>/ at the root of each
// mount (after checking the mounts have room for their share, need, and that
// no other disk is mounted inside a path), refusing names that are absolute,
// hold "..", lie outside the targets, or would be written through a symlink;
// a failure there removes the temp folders and leaves every mount untouched.
// Then each path is swapped by renames only: the old data is moved into
// .wf-restore-<id>.old/, the restored entry into its place (for a whole
// mount, every entry at its root but the temp folders), and what the backup
// excludes (it is not in the archive) is moved from the old data into the
// restored tree. A failed rename puts everything back (exit 1, nothing
// changed); only when that fails too is the restore errPartial, and the old
// data then stays in .wf-restore-<id>.old/. Paths the archive doesn't hold
// are left as they are, with a warning.
func restoreFiles(targets []target, id string, need map[string]uint64, exclude []string, in io.Reader) (Result, error) {
	return restoreFilesNotify(targets, id, need, exclude, in, nil)
}

// restoreFilesNotify is restoreFiles that calls swapping (if set) just
// before the first path is moved into place: everything before it only wrote
// into the restore's temp folders.
func restoreFilesNotify(targets []target, id string, need map[string]uint64, exclude []string, in io.Reader, swapping func()) (Result, error) {
	var warns warner
	tmpName := restoreTmpPrefix + id
	roots := map[string]string{}  // mount root -> its temp folder
	asides := map[string]string{} // mount root -> where the old data goes
	for _, t := range targets {
		roots[t.root] = filepath.Join(t.root, tmpName)
		asides[t.root] = filepath.Join(t.root, tmpName+restoreOldSuffix)
	}
	for _, t := range targets {
		if err := checkParents(t); err != nil {
			return Result{}, err
		}
	}
	if err := checkMounts(targets); err != nil {
		return Result{}, err
	}
	// Old data an earlier restore could not put back is never deleted for
	// the user: they decide.
	for root := range roots {
		if err := checkOldData(root, targets); err != nil {
			return Result{}, err
		}
	}
	// A killed restore leaves its extraction folder, possibly a whole copy:
	// it goes first, or it would eat the room the check looks for.
	for root := range roots {
		removeStale(root)
	}
	if err := checkRooms(roots, need, targets); err != nil {
		return Result{}, err
	}
	keepOld := false // the old data is all that is left of a path
	defer func() {
		for _, tmp := range roots {
			_ = os.RemoveAll(tmp)
		}
		if !keepOld {
			for _, aside := range asides {
				_ = os.RemoveAll(aside)
			}
		}
	}()
	for _, tmp := range roots {
		if err := os.Mkdir(tmp, 0o700); err != nil {
			return Result{}, fmt.Errorf("make the restore folder: %w", scrub(err))
		}
	}
	x := extraction{targets: targets, roots: roots, warn: warns.add, seen: map[int]bool{}, rootHeaders: map[string]meta{}, copies: map[string]int{}}
	n, err := x.extract(in)
	if err != nil {
		return Result{}, err
	}
	for i, t := range targets {
		if t.entry == "" {
			x.seen[i] = true // a whole volume: no entries means it was empty
		}
		if !x.seen[i] {
			warns.add(t.display() + ": not in the copy, left as it is")
		}
	}
	if swapping != nil {
		swapping()
	}
	partial, err := swap(targets, roots, asides, x.seen, exclude, warns.add)
	if err != nil {
		keepOld = partial
		if partial {
			err = fmt.Errorf("%w: %w", err, errPartial)
		}
		return Result{Warnings: warns.result()}, err
	}
	// The temp folders go before the mount roots get their own metadata: the
	// removal changes the roots' times.
	for _, tmp := range roots {
		_ = os.RemoveAll(tmp)
	}
	for _, aside := range asides {
		_ = os.RemoveAll(aside)
	}
	for root, m := range x.rootHeaders {
		if err := applyDir(root, m); err != nil {
			warns.add(fmt.Sprintf("/%s: owner, mode or times not restored: %v", m.name, scrub(err)))
		}
	}
	for root := range roots {
		syncDir(root)
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

// checkMounts refuses a path with another file system on it or inside it (a
// bind mount is recursive: a second disk mounted below the source shows up
// there). The restore swaps by renaming within the mount's own file system,
// which can't replace a mount point or move data across disks.
func checkMounts(targets []target) error {
	points, err := mountPoints()
	if err != nil {
		return fmt.Errorf("check for disks mounted inside the restore paths: %w", scrub(err))
	}
	for _, t := range targets {
		refused := fmt.Errorf("refused: a disk is mounted at or inside %s, so it can't be restored in place", t.display())
		for _, mp := range points {
			if strings.HasPrefix(mp, t.src+"/") {
				return refused
			}
		}
		if t.sub == "" {
			continue
		}
		rootDev, err := devOf(t.root)
		if err != nil {
			return fmt.Errorf("%s: %w", t.display(), scrub(err))
		}
		for p := t.src; p != t.root; p = filepath.Dir(p) {
			dev, err := devOf(p)
			if errors.Is(err, fs.ErrNotExist) {
				continue // look at the nearest folder that exists
			}
			if err != nil {
				return fmt.Errorf("%s: %w", t.display(), scrub(err))
			}
			if dev != rootDev {
				return refused
			}
			break
		}
	}
	return nil
}

// checkRooms requires each file system holding mounts of the restore to have
// room for the restored copies next to the current data. Mounts on one file
// system share its free space.
func checkRooms(roots map[string]string, need map[string]uint64, targets []target) error {
	names := make([]string, 0, len(roots))
	for root := range roots {
		names = append(names, root)
	}
	sort.Strings(names)
	groups := map[string][]string{} // device -> mount roots
	var order []string
	for _, root := range names {
		key := "root:" + root
		if dev, err := devOf(root); err == nil {
			key = strconv.FormatUint(dev, 10)
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], root)
	}
	for _, key := range order {
		var sum uint64
		for _, root := range groups[key] {
			sum += need[root]
		}
		if sum == 0 {
			continue
		}
		first := groups[key][0]
		free, err := freeSpace(first)
		if err != nil {
			return fmt.Errorf("check the free space for the restore: %w", scrub(err))
		}
		if free < sum {
			name := first
			for _, t := range targets {
				if t.root == first {
					name = t.display()
					break
				}
			}
			return fmt.Errorf("not enough free space to restore %s: it needs %s next to the current data, %s is free", name, human(sum), human(free))
		}
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

// restoreOldSuffix ends the name of the folder holding the data a restore
// replaces.
const restoreOldSuffix = ".old"

// removeStale removes the extraction folders a killed restore left at root
// (never a .old folder: see checkOldData).
func removeStale(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), restoreTmpPrefix) && !strings.HasSuffix(e.Name(), restoreOldSuffix) {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// checkOldData refuses a mount holding a .wf-restore-*.old folder with files
// in it: after a restore that failed half-way it holds the only copy of what
// could not be put back (the pre-restore point never holds the files its
// backup excludes). One without files in it is removed.
func checkOldData(root string, targets []target) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), restoreTmpPrefix) || !strings.HasSuffix(e.Name(), restoreOldSuffix) {
			continue
		}
		dir := filepath.Join(root, e.Name())
		hasFiles := false
		_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				hasFiles = true
				return filepath.SkipAll
			}
			return nil
		})
		if hasFiles {
			shown := root
			for _, t := range targets {
				if t.root == root {
					shown = t.display()
					break
				}
			}
			return fmt.Errorf("old data from an earlier incomplete restore is kept in %s at the root of the mount holding %s; move or remove it, then restore again", e.Name(), shown)
		}
		_ = os.RemoveAll(dir)
	}
	return nil
}

// meta is the owner, mode and times of an archive entry.
type meta struct {
	name         string // the entry's name, for messages
	uid, gid     int
	mode         fs.FileMode
	atime, mtime time.Time
}

func metaOf(h *tar.Header) meta {
	atime := h.AccessTime
	if atime.IsZero() {
		atime = h.ModTime
	}
	return meta{
		name: name(h), uid: h.Uid, gid: h.Gid,
		mode:  h.FileInfo().Mode() & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky),
		atime: atime, mtime: h.ModTime,
	}
}

func (m meta) times() []unix.Timespec {
	return []unix.Timespec{unix.NsecToTimespec(m.atime.UnixNano()), unix.NsecToTimespec(m.mtime.UnixNano())}
}

// applyFile gives an open file or folder its owner, mode and times, all
// through the descriptor: whatever has since appeared at its path is never
// touched (owner first: chown clears setuid bits).
func applyFile(f *os.File, m meta) error {
	if err := fchown(f, m.uid, m.gid); err != nil {
		return err
	}
	if err := f.Chmod(m.mode); err != nil {
		return err
	}
	return unix.UtimesNanoAt(unix.AT_FDCWD, "/proc/self/fd/"+strconv.Itoa(int(f.Fd())), m.times(), 0)
}

// applyDir does the same for the folder at p, which must not be a symlink.
func applyDir(p string, m meta) error {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return applyFile(f, m)
}

// applyLink gives a symlink its owner and times, without following it (a
// symlink has no mode of its own).
func applyLink(p string, m meta) error {
	if err := lchown(p, m.uid, m.gid); err != nil {
		return err
	}
	return unix.UtimesNanoAt(unix.AT_FDCWD, p, m.times(), unix.AT_SYMLINK_NOFOLLOW)
}

// extraction is one archive being extracted into the temp folders.
type extraction struct {
	targets     []target
	roots       map[string]string
	warn        func(string)
	seen        map[int]bool    // targets the archive holds entries of
	rootHeaders map[string]meta // a mount root's own entry, applied after the swap
	copies      map[string]int  // folders archived once per source, by name: how many came
	open        []openDir       // the folders being filled, shallowest first
}

// openDir is a folder whose metadata is applied once its entries are done:
// filling it changes its mtime, and a read-only one must stay writable.
type openDir struct {
	name string // its archive name
	path string
	meta meta
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
	if err := x.leave(""); err != nil {
		return 0, err
	}
	return cr.n, nil
}

// leave closes the open folders that archive entry name is not in. The dump
// walks each source depth first in lexical order, so a folder is finished as
// soon as an entry outside it comes: memory follows the tree's depth, not
// its number of folders.
func (x *extraction) leave(name string) error {
	for len(x.open) > 0 {
		d := x.open[len(x.open)-1]
		if name != "" && (name == d.name || strings.HasPrefix(name, d.name+"/")) {
			return nil
		}
		x.open = x.open[:len(x.open)-1]
		if err := applyDir(d.path, d.meta); err != nil {
			return fmt.Errorf("restore /%s: %w", d.meta.name, scrub(err))
		}
	}
	return nil
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
	if err := x.leave(name); err != nil {
		return err
	}
	i, rel, ok := x.match(name, false)
	if !ok {
		return fmt.Errorf("refused: the archive entry /%s is outside the backup's paths", name)
	}
	t := x.targets[i]
	// A nested mount's own folder is archived twice, once as the (empty)
	// mount point of the mount above it, once as the nested mount's root.
	// The dump archives its sources in the order they are given here, so the
	// first copy belongs to the target listed first.
	if h.Typeflag == tar.TypeDir && rel == "" && t.sub == "" {
		if j, outerRel, ok := x.match(name, true); ok && x.targets[j].root != t.root {
			n := x.copies[name]
			x.copies[name]++
			if (n == 0) == (j < i) {
				x.seen[j] = true
				return x.place(x.targets[j], outerRel, h, nil)
			}
		}
	}
	x.seen[i] = true
	return x.place(t, rel, h, r)
}

// place writes entry h (rel below target t) into t's temp folder.
func (x *extraction) place(t target, rel string, h *tar.Header, r io.Reader) error {
	dest := path.Join(t.sub, rel)
	if dest == "" || dest == "." {
		if h.Typeflag == tar.TypeDir {
			x.rootHeaders[t.root] = metaOf(h)
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
		x.open = append(x.open, openDir{name: name(h), path: full, meta: metaOf(h)})
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
		if err == nil {
			err = applyFile(f, metaOf(h))
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
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
		if err := applyLink(full, metaOf(h)); err != nil {
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

// mover renames paths and remembers each rename, so a failure can undo
// them.
type mover struct{ done [][2]string }

func (m *mover) move(from, to string) error {
	if err := rename(from, to); err != nil {
		return err
	}
	m.done = append(m.done, [2]string{from, to})
	return nil
}

// undo puts every moved path back, last first, trying all of them; it
// returns the first failure.
func (m *mover) undo() error {
	var first error
	for i := len(m.done) - 1; i >= 0; i-- {
		if err := rename(m.done[i][1], m.done[i][0]); err != nil && first == nil {
			first = err
		}
	}
	m.done = nil
	return first
}

// swapped is a path the swap replaced: the old data now at old (the entry
// for a path, the folder holding its entries for a whole mount) and the
// restored data at fresh.
type swapped struct {
	t          target
	old, fresh string
}

// swap moves each restored path into place, shortest first: a path inside
// another one moved with it. It is all renames within one mount: the old
// data goes aside first, so a failure can put it back. partial reports that
// it could not, or that the excluded files could not all be carried over (the
// old data then stays aside).
func swap(targets []target, roots, asides map[string]string, seen map[int]bool, exclude []string, warn func(string)) (partial bool, err error) {
	order := make([]int, 0, len(targets))
	for i := range targets {
		if seen[i] {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return len(targets[order[a]].sub) < len(targets[order[b]].sub) })
	var (
		m    mover
		done []swapped
	)
	for _, i := range order {
		t := targets[i]
		if covered(t, targets, order[:slices.Index(order, i)]) {
			continue // the same folder as, or inside, a path already swapped
		}
		aside := filepath.Join(asides[t.root], strconv.Itoa(i))
		var s swapped
		var err error
		if t.sub == "" {
			s, err = swapMount(t, roots[t.root], aside, &m)
		} else {
			s, err = swapPath(t, roots[t.root], aside, &m)
		}
		if err != nil {
			err = fmt.Errorf("%s was not replaced: %w", t.display(), scrub(err))
			if uerr := m.undo(); uerr != nil {
				return true, fmt.Errorf("%s was partly replaced: %w; putting it back failed too: %v; the old data is in %s", t.display(), scrub(err), scrub(uerr), filepath.Base(asides[t.root]))
			}
			return false, err
		}
		if s.fresh != "" {
			done = append(done, s)
		}
	}
	for _, s := range done {
		if err := keepExcluded(s, exclude, warn); err != nil {
			return true, fmt.Errorf("%s was partly replaced: the files its backup excludes were not all kept: %w; the old data is in %s", s.t.display(), scrub(err), filepath.Base(asides[s.t.root]))
		}
	}
	for root := range roots {
		syncDir(root)
	}
	return false, nil
}

// covered reports whether t is the same path as, or below, one of the
// targets before it in the same mount (a mount listed at two container
// paths gives two targets for one folder): swapping it again would move the
// restored data aside.
func covered(t target, targets []target, before []int) bool {
	for _, j := range before {
		u := targets[j]
		if u.root == t.root && (u.sub == "" || t.sub == u.sub || strings.HasPrefix(t.sub, u.sub+"/")) {
			return true
		}
	}
	return false
}

// swapPath replaces the path t.src: the old one goes to aside/old, the
// restored one is moved in.
func swapPath(t target, tmp, aside string, m *mover) (swapped, error) {
	src := filepath.Join(tmp, t.sub)
	if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
		return swapped{}, nil // moved with a path above it
	}
	s := swapped{t: t, fresh: t.src}
	switch _, err := os.Lstat(t.src); {
	case err == nil:
		if err := os.MkdirAll(aside, 0o700); err != nil {
			return swapped{}, err
		}
		s.old = filepath.Join(aside, "old")
		if err := m.move(t.src, s.old); err != nil {
			return swapped{}, err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return swapped{}, err
	}
	if err := mkdirLike(t.root, filepath.Dir(t.src)); err != nil {
		return swapped{}, err
	}
	if err := m.move(src, t.src); err != nil {
		return swapped{}, err
	}
	return s, nil
}

// mkdirLike makes the missing folders down to dir, each with the mode and
// owner of the nearest folder that exists (a deleted parent of a backup path
// must stay writable for the app that owns it).
func mkdirLike(root, dir string) error {
	if _, err := os.Lstat(dir); err == nil || dir == root || dir == filepath.Dir(dir) {
		return nil
	}
	parent := filepath.Dir(dir)
	if err := mkdirLike(root, parent); err != nil {
		return err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	// Owner and mode through a descriptor, never following a symlink that
	// has since taken the folder's place.
	f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if err := fchown(f, int(st.Uid), int(st.Gid)); err != nil {
			return err
		}
	}
	return f.Chmod(info.Mode() & (fs.ModePerm | fs.ModeSetgid | fs.ModeSticky))
}

// swapMount replaces everything at a mount's root (but restore temp
// folders) with tmp's entries.
func swapMount(t target, tmp, aside string, m *mover) (swapped, error) {
	old, err := os.ReadDir(t.root)
	if err != nil {
		return swapped{}, err
	}
	if err := os.MkdirAll(aside, 0o700); err != nil {
		return swapped{}, err
	}
	for _, e := range old {
		if strings.HasPrefix(e.Name(), restoreTmpPrefix) {
			continue
		}
		if err := m.move(filepath.Join(t.root, e.Name()), filepath.Join(aside, e.Name())); err != nil {
			return swapped{}, err
		}
	}
	restored, err := os.ReadDir(tmp)
	if err != nil {
		return swapped{}, err
	}
	for _, e := range restored {
		if err := m.move(filepath.Join(tmp, e.Name()), filepath.Join(t.root, e.Name())); err != nil {
			return swapped{}, err
		}
	}
	return swapped{t: t, old: aside, fresh: t.root}, nil
}

// keepExcluded moves the old entries the backup's exclude patterns match
// (they are not in the archive: the copy never held them) into the same
// place of the restored tree. An entry the copy has too is not replaced.
func keepExcluded(s swapped, exclude []string, warn func(string)) error {
	if len(exclude) == 0 || s.old == "" {
		return nil
	}
	// The restored path must be a real folder: files are never moved through
	// a symlink there.
	freshIsDir := false
	if info, err := os.Lstat(s.fresh); err == nil && info.IsDir() {
		freshIsDir = true
	}
	return filepath.WalkDir(s.old, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(s.old, p)
		if rel == "." {
			return nil
		}
		entry := path.Join(s.t.entry, filepath.ToSlash(rel))
		if !excluded(entry, d.IsDir(), exclude) {
			return nil
		}
		skip := func() error {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !freshIsDir {
			warn("/" + entry + ": not kept, the restored copy is not a folder")
			return skip()
		}
		dest := filepath.Join(s.fresh, rel)
		if err := keepParents(s.old, s.fresh, filepath.Dir(rel)); err != nil {
			if errors.Is(err, errConflict) {
				warn("/" + entry + ": not kept, the restored copy has a file where its folder was")
				return skip()
			}
			return err
		}
		if _, err := os.Lstat(dest); err == nil {
			warn("/" + entry + ": not kept, the restored copy has one")
			return skip()
		}
		if err := rename(p, dest); err != nil {
			return err
		}
		return skip()
	})
}

var errConflict = errors.New("conflict")

// keepParents makes the folders above rel in the restored tree where the
// copy lacks them, with the old folders' owner, mode and times.
func keepParents(oldBase, fresh, rel string) error {
	if rel == "." {
		return nil
	}
	p := ""
	for _, part := range strings.Split(rel, "/") {
		p = filepath.Join(p, part)
		switch info, err := os.Lstat(filepath.Join(fresh, p)); {
		case err == nil && info.IsDir():
			continue
		case err == nil:
			return errConflict
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
		old, err := os.Lstat(filepath.Join(oldBase, p))
		if err != nil {
			return err
		}
		dir := filepath.Join(fresh, p)
		if err := os.Mkdir(dir, 0o700); err != nil {
			return err
		}
		m := meta{name: p, mode: old.Mode() & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky), atime: old.ModTime(), mtime: old.ModTime()}
		if st, ok := old.Sys().(*syscall.Stat_t); ok {
			m.uid, m.gid = int(st.Uid), int(st.Gid)
		}
		if err := applyDir(dir, m); err != nil {
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
