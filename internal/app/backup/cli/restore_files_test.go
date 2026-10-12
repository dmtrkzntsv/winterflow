package cli

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"winterflow/internal/infra/backup/helper"
)

// dumpOf archives sources the way a backup run does.
func dumpOf(t *testing.T, args ...string) []byte {
	t.Helper()
	code, out, res := run(t, append([]string{"dump", "files"}, args...)...)
	if code != ExitOK {
		t.Fatalf("dump: %d %+v", code, res)
	}
	return out
}

// snapshot is a tree as restore must reproduce it: every entry's kind,
// mode, mtime, link target and content.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		v := info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, _ := os.Readlink(p)
			v += " -> " + link
		case info.Mode().IsRegular():
			body, _ := os.ReadFile(p)
			v += " " + info.ModTime().UTC().Format(time.RFC3339Nano) + " " + string(body)
		case info.IsDir() && rel != ".":
			v += " " + info.ModTime().UTC().Format(time.RFC3339Nano)
		}
		out[rel] = v
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A two-mount backup restores over changed data: ownership, modes, mtimes
// and symlinks come back, files of the mounts outside the backup's paths
// are left alone, and no temp folder remains.
func TestRestoreFilesRoundTrip(t *testing.T) {
	src0, src1 := filesTree(t)
	if err := os.Remove(filepath.Join(src0, "media", "pipe")); err != nil {
		t.Fatal(err)
	}
	archive := dumpOf(t, "--src", src0+"/media", "--entry", "app/media", "--src", src1+"/config", "--entry", "data/config")
	before0, before1 := snapshot(t, filepath.Join(src0, "media")), snapshot(t, filepath.Join(src1, "config"))

	// The app changes things after the copy.
	if err := os.WriteFile(filepath.Join(src0, "media", "a.jpg"), []byte("edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src0, "media", "new.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(src1, "config")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src0, "other.txt"), []byte("sibling, changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	chowns := recordChowns(t)
	code, res := restore(t, archive, "files", "--id", "t1",
		"--root", src0, "--src", src0+"/media", "--entry", "app/media",
		"--root", src1, "--src", src1+"/config", "--entry", "/data/config/",
		"--need", src0+"=1000", "--need", src1+"=10")
	if code != ExitOK || res.RawSize != int64(len(archive)) || len(res.Warnings) != 0 {
		t.Fatalf("restore: %d %+v", code, res)
	}
	if after := snapshot(t, filepath.Join(src0, "media")); !mapsEqual(after, before0) {
		t.Fatalf("media:\n%v\nwant\n%v", after, before0)
	}
	if after := snapshot(t, filepath.Join(src1, "config")); !mapsEqual(after, before1) {
		t.Fatalf("config:\n%v\nwant\n%v", after, before1)
	}
	if body, _ := os.ReadFile(filepath.Join(src0, "other.txt")); string(body) != "sibling, changed" {
		t.Fatalf("a file outside the backup's paths changed: %q", body)
	}
	for _, root := range []string{src0, src1} {
		if left, _ := filepath.Glob(filepath.Join(root, restoreTmpPrefix+"*")); len(left) != 0 {
			t.Fatalf("temp folders left: %v", left)
		}
	}
	if c, ok := chowns.of("a.jpg"); !ok || c != [2]int{os.Getuid(), os.Getgid()} {
		t.Fatalf("a.jpg owner = %v %v", c, ok)
	}
	if _, ok := chowns.of("link.jpg"); !ok {
		t.Fatal("the symlink's owner was not restored")
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// The whole-volume form replaces every top-level entry of the volume.
func TestRestoreFilesWholeVolume(t *testing.T) {
	src0, _ := filesTree(t)
	if err := os.Remove(filepath.Join(src0, "media", "pipe")); err != nil {
		t.Fatal(err)
	}
	archive := dumpOf(t, "--src", src0, "--entry", "")
	before := snapshot(t, src0)
	if err := os.RemoveAll(filepath.Join(src0, "media")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src0, "extra.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A temp folder a killed restore left behind is cleaned up.
	if err := os.MkdirAll(filepath.Join(src0, restoreTmpPrefix+"old", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	recordChowns(t)
	if code, res := restore(t, archive, "files", "--id", "t2", "--root", src0, "--src", src0, "--entry", ""); code != ExitOK {
		t.Fatalf("%d %+v", code, res)
	}
	if after := snapshot(t, src0); !mapsEqual(after, before) {
		t.Fatalf("volume:\n%v\nwant\n%v", snapshot(t, src0), before)
	}
}

// A nested mount (a volume at /data/media inside the one at /data) is a
// second source: the dump archives data/media/ twice (the empty mount point
// in the outer volume, then the inner volume's root). Restore takes both and
// keeps the mount point in the outer volume, whichever source was dumped
// first.
func TestRestoreFilesNestedMount(t *testing.T) {
	for name, innerFirst := range map[string]bool{"outer first": false, "inner first": true} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			outer, inner := filepath.Join(root, "0"), filepath.Join(root, "1")
			for _, f := range []struct{ path, body string }{
				{"0/app.yml", "k: v"}, {"0/sub/notes.txt", "notes"}, {"1/photo.jpg", "jpeg"}, {"1/2026/a.jpg", "a"},
			} {
				writeFileT(t, root, f.path, f.body)
			}
			// The mount point and the inner root differ, so a mix-up shows.
			if err := os.Mkdir(filepath.Join(outer, "media"), 0o711); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(outer, "media"), 0o711); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(inner, 0o750); err != nil {
				t.Fatal(err)
			}
			pointTime, rootTime := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC), time.Date(2021, 6, 7, 8, 9, 10, 0, time.UTC)
			if err := os.Chtimes(filepath.Join(outer, "media"), pointTime, pointTime); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(inner, rootTime, rootTime); err != nil {
				t.Fatal(err)
			}
			args := []string{"--src", outer, "--entry", "data", "--src", inner, "--entry", "data/media"}
			restoreArgs := []string{"--root", outer, "--src", outer, "--entry", "data", "--root", inner, "--src", inner, "--entry", "data/media"}
			if innerFirst {
				args = []string{"--src", inner, "--entry", "data/media", "--src", outer, "--entry", "data"}
				restoreArgs = []string{"--root", inner, "--src", inner, "--entry", "data/media", "--root", outer, "--src", outer, "--entry", "data"}
			}
			archive := dumpOf(t, args...)
			dirs := 0
			for tr := tar.NewReader(bytes.NewReader(archive)); ; {
				h, err := tr.Next()
				if err != nil {
					break
				}
				if h.Name == "data/media/" {
					dirs++
				}
			}
			if dirs != 2 {
				t.Fatalf("the dump archived data/media/ %d times, want 2", dirs)
			}
			beforeOuter, beforeInner := snapshot(t, outer), snapshot(t, inner)
			for _, p := range []string{filepath.Join(outer, "sub"), filepath.Join(inner, "2026"), filepath.Join(outer, "media")} {
				if err := os.RemoveAll(p); err != nil {
					t.Fatal(err)
				}
			}
			writeFileT(t, root, "1/stray.jpg", "x")
			if err := os.Chmod(inner, 0o700); err != nil {
				t.Fatal(err)
			}
			recordChowns(t)
			code, res := restore(t, archive, append([]string{"files", "--id", "n1"}, restoreArgs...)...)
			if code != ExitOK || len(res.Warnings) != 0 {
				t.Fatalf("%d %+v", code, res)
			}
			if after := snapshot(t, outer); !mapsEqual(after, beforeOuter) {
				t.Fatalf("outer:\n%v\nwant\n%v", after, beforeOuter)
			}
			if after := snapshot(t, inner); !mapsEqual(after, beforeInner) {
				t.Fatalf("inner:\n%v\nwant\n%v", after, beforeInner)
			}
			if info, err := os.Lstat(inner); err != nil || info.Mode().Perm() != 0o750 || !info.ModTime().Equal(rootTime) {
				t.Fatalf("the inner mount's root: %v %v", info, err)
			}
			if info, err := os.Lstat(filepath.Join(outer, "media")); err != nil || info.Mode().Perm() != 0o711 || !info.ModTime().Equal(pointTime) {
				t.Fatalf("the outer mount point: %v %v", info, err)
			}
		})
	}
}

// writeFileT writes body at root/rel, making its folders.
func writeFileT(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// tarOf builds an archive by hand.
func tarOf(t *testing.T, entries ...*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range entries {
		body := h.PAXRecords["body"]
		h.PAXRecords = nil
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
		}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func file(name, body string) *tar.Header {
	return &tar.Header{Name: name, Typeflag: tar.TypeReg, PAXRecords: map[string]string{"body": body}}
}

// An archive that is malformed, leaves the backup's paths or writes through
// a symlink is refused before anything is swapped: the mount is untouched
// and no temp folder remains.
func TestRestoreFilesRefusals(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "media/keep.txt", "original")
	target := t.TempDir()
	good := file("app/media/new.txt", "new")
	for name, tc := range map[string]struct {
		archive []byte
		want    string
	}{
		"dot-dot":   {tarOf(t, good, file("app/media/../../etc/passwd", "x")), "is not a relative path"},
		"parent":    {tarOf(t, file("../x", "x")), "is not a relative path"},
		"absolute":  {tarOf(t, file("/app/media/x", "x")), "is not a relative path"},
		"outside":   {tarOf(t, good, file("app/other/x", "x")), "outside the backup's paths"},
		"prefix":    {tarOf(t, file("app/mediax/x", "x")), "outside the backup's paths"},
		"truncated": {tarOf(t, good)[:700], "read the archive"},
		"through a symlink": {tarOf(t,
			&tar.Header{Name: "app/media/link", Typeflag: tar.TypeSymlink, Linkname: target},
			file("app/media/link/evil", "x")), "through a symlink"},
		"symlink over a folder": {tarOf(t,
			&tar.Header{Name: "app/media/d/", Typeflag: tar.TypeDir, Mode: 0o755},
			&tar.Header{Name: "app/media/d", Typeflag: tar.TypeSymlink, Linkname: "x"}), "is a folder and a symlink"},
		"file over a folder": {tarOf(t,
			&tar.Header{Name: "app/media/d/", Typeflag: tar.TypeDir, Mode: 0o755},
			file("app/media/d", "x")), "through a symlink or over a folder"},
		"over a symlink": {tarOf(t,
			&tar.Header{Name: "app/media/link", Typeflag: tar.TypeSymlink, Linkname: filepath.Join(target, "f")},
			file("app/media/link", "x")), "through a symlink"},
	} {
		recordChowns(t)
		code, res := restore(t, tc.archive, "files", "--id", "r1", "--root", root, "--src", root+"/media", "--entry", "app/media")
		if code != ExitFailed || !strings.Contains(res.Error, tc.want) {
			t.Errorf("%s: %d %+v, want %q", name, code, res, tc.want)
		}
		if body, err := os.ReadFile(filepath.Join(root, "media", "keep.txt")); err != nil || string(body) != "original" {
			t.Fatalf("%s: the mount changed: %q %v", name, body, err)
		}
		if _, err := os.Stat(filepath.Join(root, "media", "new.txt")); !os.IsNotExist(err) {
			t.Fatalf("%s: a file was restored", name)
		}
		if left, _ := filepath.Glob(filepath.Join(root, restoreTmpPrefix+"*")); len(left) != 0 {
			t.Fatalf("%s: temp folders left: %v", name, left)
		}
		if entries, _ := os.ReadDir(target); len(entries) != 0 {
			t.Fatalf("%s: wrote through the symlink", name)
		}
	}
}

// A mount without room for the copy beside the current data is refused
// before anything is written.
func TestRestoreFilesNeedsRoom(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "media/keep.txt", "original")
	prev := freeSpace
	freeSpace = func(p string) (uint64, error) {
		if p != root {
			t.Errorf("free space asked of %s", p)
		}
		return 1 << 20, nil
	}
	t.Cleanup(func() { freeSpace = prev })
	code, res := restore(t, tarOf(t, file("app/media/new.txt", "new")), "files", "--id", "r2",
		"--root", root, "--src", root+"/media", "--entry", "app/media", "--need", root+"=3000000")
	if code != ExitFailed || !strings.Contains(res.Error, "not enough free space to restore /app/media: it needs 2.9 MiB next to the current data, 1.0 MiB is free") {
		t.Fatalf("%d %+v", code, res)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Fatalf("the mount changed: %v", entries)
	}
	if got := human(512); got != "512 B" {
		t.Fatalf("human(512) = %s", got)
	}
}

// A path the copy doesn't hold is left as it is, with a warning; anything
// that is not a file, folder or symlink is skipped; a file the dump padded
// (it shrank while archived) comes back at its archived size.
func TestRestoreFilesMissingPathAndOddEntries(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "media/a.txt", "old")
	writeFileT(t, root, "config/c.yml", "untouched")
	recordChowns(t)
	archive := tarOf(t,
		&tar.Header{Name: "app/media/", Typeflag: tar.TypeDir, Mode: 0o755},
		file("app/media/a.txt", "abc\x00\x00"),
		&tar.Header{Name: "app/media/dev", Typeflag: tar.TypeChar},
		&tar.Header{Name: "app/media/" + restoreTmpPrefix + "x/", Typeflag: tar.TypeDir, Mode: 0o755},
	)
	code, res := restore(t, archive, "files", "--id", "r3",
		"--root", root, "--src", root+"/media", "--entry", "app/media",
		"--root", root, "--src", root+"/config", "--entry", "app/config")
	if code != ExitOK || len(res.Warnings) != 2 || !strings.Contains(res.Warnings[0], "/app/media/dev: skipped") ||
		res.Warnings[1] != "/app/config: not in the copy, left as it is" {
		t.Fatalf("%d %+v", code, res)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "media", "a.txt")); string(body) != "abc\x00\x00" {
		t.Fatalf("a.txt = %q", body)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "config", "c.yml")); string(body) != "untouched" {
		t.Fatalf("c.yml = %q", body)
	}
}

// failRenames makes the helper's renames fail from the nth call on (1 = the
// first); until then they work.
func failRenames(t *testing.T, from, to int) *int {
	t.Helper()
	prev := rename
	calls := 0
	rename = func(a, b string) error {
		calls++
		if calls >= from && calls <= to {
			return syscall.EIO
		}
		return prev(a, b)
	}
	t.Cleanup(func() { rename = prev })
	return &calls
}

// A failure while swapping that can be undone changes nothing (exit 1): the
// old data of every path is back and no temp folder remains.
func TestRestoreFilesSwapFailureIsUndone(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "media/keep.txt", "original")
	writeFileT(t, root, "config/c.yml", "untouched")
	archive := tarOf(t, file("app/media/new.txt", "new"), file("app/config/new.yml", "new"))
	recordChowns(t)
	// media: old aside (1), new in (2); config: old aside (3) fails; undo works.
	failRenames(t, 3, 3)
	code, res := restore(t, archive, "files", "--id", "r4",
		"--root", root, "--src", root+"/media", "--entry", "app/media",
		"--root", root, "--src", root+"/config", "--entry", "app/config")
	if code != ExitFailed || !strings.Contains(res.Error, "/app/config was not replaced") {
		t.Fatalf("%d %+v", code, res)
	}
	for p, want := range map[string]string{"media/keep.txt": "original", "config/c.yml": "untouched"} {
		if body, err := os.ReadFile(filepath.Join(root, p)); err != nil || string(body) != want {
			t.Fatalf("%s = %q %v", p, body, err)
		}
	}
	for _, p := range []string{"media/new.txt", "config/new.yml"} {
		if _, err := os.Stat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Fatalf("%s was restored", p)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(root, restoreTmpPrefix+"*")); len(left) != 0 {
		t.Fatalf("temp folders left: %v", left)
	}
}

// A failure while swapping that can't be undone reports a partial restore
// and keeps the old data aside.
func TestRestoreFilesPartialSwap(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "a/b/old.txt", "old")
	recordChowns(t)
	failRenames(t, 2, 1000) // the new data can't go in, and the old can't come back
	code, res := restore(t, tarOf(t, file("x/new.txt", "new")), "files", "--id", "r4",
		"--root", root, "--src", root+"/a/b", "--entry", "x")
	if code != ExitPartial || !strings.Contains(res.Error, "/x was partly replaced") || !strings.Contains(res.Error, ".wf-restore-r4.old") {
		t.Fatalf("%d %+v", code, res)
	}
	if body, err := os.ReadFile(filepath.Join(root, ".wf-restore-r4.old", "0", "old", "old.txt")); err != nil || string(body) != "old" {
		t.Fatalf("the old data was not kept: %q %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".wf-restore-r4")); !os.IsNotExist(err) {
		t.Fatalf("the extracted copy was kept: %v", err)
	}
}

// A path below a symlink is refused before extraction.
func TestRestoreFilesRefusesASymlinkedParent(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "a")); err != nil {
		t.Fatal(err)
	}
	code, res := restore(t, tarOf(t, file("x/new.txt", "new")), "files", "--id", "r5",
		"--root", root, "--src", root+"/a/b", "--entry", "x")
	if code != ExitFailed || res.Error != "refused: a folder above /x is not a folder (a symlink?), so it can't be restored there" {
		t.Fatalf("%d %+v", code, res)
	}
}

func TestRestoreFilesUsage(t *testing.T) {
	for _, args := range [][]string{
		{"files"}, {"files", "--id", "x", "--src", "/a", "--entry", "e"},
		{"files", "--root", "/a", "--src", "/a", "--entry", "e"},
		{"files", "--id", "Bad_Id", "--root", "/a", "--src", "/a", "--entry", "e"},
		{"files", "--id", "x", "--root", "/a", "--src", "/b", "--entry", "e"},
		{"files", "--id", "x", "--root", "rel", "--src", "rel/x", "--entry", "e"},
		{"files", "--id", "x", "--root", "/a", "--src", "/a", "--entry", "e", "--need", "/a"},
		{"files", "--id", "x", "--root", "/a", "--src", "/a", "--entry", "e", "--need", "/a=lots"},
	} {
		var stderr bytes.Buffer
		if code := Run(append([]string{"restore"}, args...), strings.NewReader(""), io.Discard, &stderr); code != ExitUsage || !strings.Contains(stderr.String(), "usage:") {
			t.Errorf("%v: code %d, stderr %q", args, code, stderr.String())
		}
	}
}

// A temp folder a killed restore left holds up to a whole copy: it is removed
// before the room is checked, so it can't make every later restore fail.
func TestRestoreFilesRemovesStaleFoldersBeforeTheRoomCheck(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "media/keep.txt", "original")
	writeFileT(t, root, restoreTmpPrefix+"old/big", "x")
	prev := freeSpace
	freeSpace = func(string) (uint64, error) { return 1 << 20, nil }
	t.Cleanup(func() { freeSpace = prev })
	code, res := restore(t, tarOf(t, file("app/media/new.txt", "new")), "files", "--id", "s1",
		"--root", root, "--src", root+"/media", "--entry", "app/media", "--need", root+"=3000000")
	if code != ExitFailed || !strings.Contains(res.Error, "not enough free space") {
		t.Fatalf("%d %+v", code, res)
	}
	if left, _ := filepath.Glob(filepath.Join(root, restoreTmpPrefix+"*")); len(left) != 0 {
		t.Fatalf("the stale folder is still there: %v", left)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "media", "keep.txt")); string(body) != "original" {
		t.Fatalf("the mount changed: %q", body)
	}
}

// Mounts on one file system share its free space.
func TestRestoreFilesSharedDiskRoom(t *testing.T) {
	a, b := t.TempDir(), t.TempDir() // the same file system
	var asked []string
	prev := freeSpace
	freeSpace = func(p string) (uint64, error) { asked = append(asked, p); return 10 << 20, nil }
	t.Cleanup(func() { freeSpace = prev })
	code, res := restore(t, tarOf(t, file("a/x", "x"), file("b/x", "x")), "files", "--id", "s2",
		"--root", a, "--src", a, "--entry", "a", "--root", b, "--src", b, "--entry", "b",
		"--need", a+"=6291456", "--need", b+"=6291456")
	if code != ExitFailed || !strings.Contains(res.Error, "it needs 12.0 MiB next to the current data, 10.0 MiB is free") || len(asked) != 1 {
		t.Fatalf("%d %+v asked %v", code, res, asked)
	}
}

// What the backup excludes isn't in the copy; the restore keeps those files
// where they are, in the restored tree, instead of deleting them with the
// old data.
func TestRestoreFilesKeepsExcludedFiles(t *testing.T) {
	root := t.TempDir()
	for p, body := range map[string]string{
		"media/a.jpg": "a", "media/big.mp4": "movie", "media/sub/clip.mp4": "clip", "media/sub/b.jpg": "b",
		"media/cache/x": "cached", "media/new-but-kept/note": "n",
	} {
		writeFileT(t, root, p, body)
	}
	excludes := []string{"--exclude", "*.mp4", "--exclude", "cache/"}
	archive := dumpOf(t, append([]string{"--src", root + "/media", "--entry", "app/media"}, excludes...)...)
	// The app changes things after the copy.
	if err := os.WriteFile(filepath.Join(root, "media", "a.jpg"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "media", "sub", "b.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "media", "new-but-kept")); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, root, "media/later/clip2.mp4", "later") // in a folder the copy lacks
	writeFileT(t, root, "media/new.txt", "not excluded")
	recordChowns(t)
	args := append([]string{"files", "--id", "k1", "--root", root, "--src", root + "/media", "--entry", "app/media"}, excludes...)
	if code, res := restore(t, archive, args...); code != ExitOK || len(res.Warnings) != 0 {
		t.Fatalf("%d %+v", code, res)
	}
	for p, want := range map[string]string{
		"media/a.jpg": "a", "media/big.mp4": "movie", "media/sub/clip.mp4": "clip", "media/sub/b.jpg": "b",
		"media/cache/x": "cached", "media/later/clip2.mp4": "later",
	} {
		if body, err := os.ReadFile(filepath.Join(root, p)); err != nil || string(body) != want {
			t.Errorf("%s = %q %v, want %q", p, body, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "media", "new.txt")); !os.IsNotExist(err) {
		t.Errorf("a file the backup does not exclude survived: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(root, restoreTmpPrefix+"*")); len(left) != 0 {
		t.Fatalf("temp folders left: %v", left)
	}
}

// The same for a whole volume, where the restored entries replace every
// top-level entry of the mount.
func TestRestoreFilesKeepsExcludedFilesOfAWholeVolume(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "a.mp4", "movie")
	writeFileT(t, root, "b.txt", "b")
	archive := dumpOf(t, "--src", root, "--entry", "", "--exclude", "*.mp4")
	writeFileT(t, root, "b.txt", "changed")
	recordChowns(t)
	if code, res := restore(t, archive, "files", "--id", "k2", "--root", root, "--src", root, "--entry", "", "--exclude", "*.mp4"); code != ExitOK || len(res.Warnings) != 0 {
		t.Fatalf("%d %+v", code, res)
	}
	for p, want := range map[string]string{"a.mp4": "movie", "b.txt": "b"} {
		if body, err := os.ReadFile(filepath.Join(root, p)); err != nil || string(body) != want {
			t.Errorf("%s = %q %v", p, body, err)
		}
	}
}

// A kept file never replaces one the copy has, and a file where the old tree
// had a folder keeps its files out with a warning.
func TestRestoreFilesKeptFilesNeverReplaceTheCopy(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "media/a.mp4", "current")
	writeFileT(t, root, "media/d/x.mp4", "inside")
	recordChowns(t)
	code, res := restore(t, tarOf(t, file("app/media/a.mp4", "copy"), file("app/media/d", "now a file")), "files", "--id", "k3",
		"--root", root, "--src", root+"/media", "--entry", "app/media", "--exclude", "*.mp4")
	if code != ExitOK || len(res.Warnings) != 2 ||
		res.Warnings[0] != "/app/media/a.mp4: not kept, the restored copy has one" ||
		res.Warnings[1] != "/app/media/d/x.mp4: not kept, the restored copy has a file where its folder was" {
		t.Fatalf("%d %+v", code, res)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "media", "a.mp4")); string(body) != "copy" {
		t.Fatalf("a.mp4 = %q", body)
	}
}

// A disk mounted inside a path (a bind mount is recursive) can't be swapped
// by renames: refused before anything is touched.
func TestRestoreFilesRefusesMountsInside(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "media/keep.txt", "original")
	prevMounts, prevDev := mountPoints, devOf
	t.Cleanup(func() { mountPoints, devOf = prevMounts, prevDev })
	for name, tc := range map[string]struct {
		mounts []string
		dev    func(string) (uint64, error)
		src    string
		entry  string
	}{
		"below the path":      {[]string{"/", root, root + "/media/disk2"}, prevDev, root + "/media", "app/media"},
		"below a whole mount": {[]string{root + "/disk2"}, prevDev, root, ""},
		"the path is a mount": {nil, func(p string) (uint64, error) {
			if p == root+"/media" {
				return 99, nil
			}
			return prevDev(p)
		}, root + "/media", "app/media"},
		"its nearest parent is a mount": {nil, func(p string) (uint64, error) {
			if p == root+"/media" {
				return 99, nil
			}
			return prevDev(p)
		}, root + "/media/not/yet", "app/media/not/yet"},
	} {
		mountPoints = func() ([]string, error) { return tc.mounts, nil }
		devOf = tc.dev
		recordChowns(t)
		code, res := restore(t, tarOf(t, file(tc.entry+"/new.txt", "new")), "files", "--id", "m1", "--root", root, "--src", tc.src, "--entry", tc.entry)
		want := "refused: a disk is mounted at or inside /" + tc.entry + ", so it can't be restored in place"
		if tc.entry == "" {
			want = "refused: a disk is mounted at or inside the volume, so it can't be restored in place"
		}
		if code != ExitFailed || res.Error != want {
			t.Errorf("%s: %d %+v", name, code, res)
		}
		if body, _ := os.ReadFile(filepath.Join(root, "media", "keep.txt")); string(body) != "original" {
			t.Fatalf("%s: the mount changed", name)
		}
		if left, _ := filepath.Glob(filepath.Join(root, restoreTmpPrefix+"*")); len(left) != 0 {
			t.Fatalf("%s: temp folders left: %v", name, left)
		}
	}
	mountPoints = func() ([]string, error) { return nil, os.ErrPermission }
	if code, res := restore(t, tarOf(t, file("app/media/new.txt", "new")), "files", "--id", "m2", "--root", root, "--src", root+"/media", "--entry", "app/media"); code != ExitFailed ||
		!strings.Contains(res.Error, "check for disks mounted inside the restore paths") {
		t.Fatalf("unreadable mountinfo: %d %+v", code, res)
	}
}

func TestParseMountInfo(t *testing.T) {
	raw := "22 1 8:1 / / rw,relatime - ext4 /dev/sda1 rw\n36 35 98:0 /mnt1 /mnt/with\\040space rw - ext3 /dev/root rw\n\nshort\n37 35 98:0 / /a\\134b\\012c rw - tmpfs t rw\n"
	got := parseMountInfo(raw)
	if want := []string{"/", "/mnt/with space", "/a\\b\nc"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("mount points = %q, want %q", got, want)
	}
	if got := unescapeMount(`/x\04`); got != `/x\04` {
		t.Fatalf("a truncated escape = %q", got)
	}
	if _, err := mountPoints(); err != nil {
		t.Fatalf("/proc/self/mountinfo: %v", err)
	}
}

// Owner, mode and times are applied through descriptors or without following
// symlinks: a symlink entry gets its own times, and what it points at (here a
// file outside the restore) is never touched.
func TestRestoreFilesNeverFollowsSymlinksForMetadata(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("v"), 0o640); err != nil {
		t.Fatal(err)
	}
	victimTime := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(victim, victimTime, victimTime); err != nil {
		t.Fatal(err)
	}
	linkTime := time.Date(2022, 2, 3, 4, 5, 6, 0, time.UTC)
	logs := recordChowns(t)
	code, res := restore(t, tarOf(t,
		&tar.Header{Name: "app/media/link", Typeflag: tar.TypeSymlink, Linkname: victim, ModTime: linkTime, Mode: 0o777, Uid: 7, Gid: 8},
		file("app/media/f", "f")), "files", "--id", "l1", "--root", root, "--src", root+"/media", "--entry", "app/media")
	if code != ExitOK {
		t.Fatalf("%d %+v", code, res)
	}
	info, err := os.Lstat(filepath.Join(root, "media", "link"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 || !info.ModTime().Equal(linkTime) {
		t.Fatalf("the link: %v %v", info, err)
	}
	if c, ok := logs.of("link"); !ok || c != [2]int{7, 8} {
		t.Fatalf("link owner = %v %v", c, ok)
	}
	vinfo, err := os.Stat(victim)
	if err != nil || vinfo.Mode().Perm() != 0o640 || !vinfo.ModTime().Equal(victimTime) {
		t.Fatalf("the file behind the link changed: %v %v", vinfo, err)
	}
}

// A whole-volume copy with nothing in it (the volume was empty) empties the
// volume.
func TestRestoreFilesEmptyVolume(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "keep.txt", "newer")
	recordChowns(t)
	if code, res := restore(t, tarOf(t), "files", "--id", "e1", "--root", root, "--src", root, "--entry", ""); code != ExitOK || len(res.Warnings) != 0 {
		t.Fatalf("%d %+v", code, res)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("the volume still holds %v", entries)
	}
}

// A mount's root takes the copy's mode and times, which removing the temp
// folders must not disturb; a restore's own temp folder in the archive is
// skipped.
func TestRestoreFilesMountRootTimesAndTempNames(t *testing.T) {
	root := t.TempDir()
	when := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	recordChowns(t)
	code, res := restore(t, tarOf(t,
		&tar.Header{Name: "app/", Typeflag: tar.TypeDir, Mode: 0o750, ModTime: when},
		&tar.Header{Name: "app/" + restoreTmpPrefix + "x/", Typeflag: tar.TypeDir, Mode: 0o755},
		file("app/ok.txt", "ok")), "files", "--id", "e2", "--root", root, "--src", root, "--entry", "app")
	if code != ExitOK || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "skipped (a restore's temp folder)") {
		t.Fatalf("%d %+v", code, res)
	}
	if info, err := os.Lstat(root); err != nil || info.Mode().Perm() != 0o750 || !info.ModTime().Equal(when) {
		t.Fatalf("the root: %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(root, "ok.txt")); err != nil {
		t.Fatal(err)
	}
}

// A path restored below another restored path moves with it; a folder the
// path needs is made with its parent's owner and mode.
func TestRestoreFilesNestedPathsAndMissingParents(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "a/old.txt", "old")
	writeFileT(t, root, "a/b/old2.txt", "old2")
	logs := recordChowns(t)
	code, res := restore(t, tarOf(t, file("app/a/x.txt", "x"), file("app/a/b/y.txt", "y"), file("app/p/q/z.txt", "z")), "files", "--id", "n2",
		"--root", root, "--src", root+"/a", "--entry", "app/a",
		"--root", root, "--src", root+"/a/b", "--entry", "app/a/b",
		"--root", root, "--src", root+"/p/q", "--entry", "app/p/q")
	if code != ExitOK || len(res.Warnings) != 0 {
		t.Fatalf("%d %+v", code, res)
	}
	for p, want := range map[string]string{"a/x.txt": "x", "a/b/y.txt": "y", "p/q/z.txt": "z"} {
		if body, err := os.ReadFile(filepath.Join(root, p)); err != nil || string(body) != want {
			t.Errorf("%s = %q %v", p, body, err)
		}
	}
	for _, p := range []string{"a/old.txt", "a/b/old2.txt"} {
		if _, err := os.Stat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Errorf("%s survived", p)
		}
	}
	rootInfo, _ := os.Stat(root)
	if info, err := os.Stat(filepath.Join(root, "p")); err != nil || info.Mode().Perm() != rootInfo.Mode().Perm() {
		t.Fatalf("p: %v %v", info, err)
	}
	if c, ok := logs.of("p"); !ok || c != [2]int{os.Getuid(), os.Getgid()} {
		t.Fatalf("p's owner = %v %v", c, ok)
	}
}

// Old data an incomplete restore (exit 5) kept aside is never deleted by the
// next one: that restore is refused and the folder stays. A .old folder with
// no files in it is just removed, and so is a plain extraction folder.
func TestRestoreFilesNeverDeletesOldData(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "a/b/old.txt", "old")
	recordChowns(t)
	failRenames(t, 2, 1000)
	args := []string{"files", "--id", "r4", "--root", root, "--src", root + "/a/b", "--entry", "x"}
	if code, _ := restore(t, tarOf(t, file("x/new.txt", "new")), args...); code != ExitPartial {
		t.Fatalf("first restore: %d", code)
	}
	rename = os.Rename
	kept := filepath.Join(root, ".wf-restore-r4.old", "0", "old", "old.txt")
	args[2] = "r5"
	code, res := restore(t, tarOf(t, file("x/new.txt", "new")), args...)
	if code != ExitFailed || !strings.Contains(res.Error, "old data from an earlier incomplete restore is kept in .wf-restore-r4.old") ||
		!strings.Contains(res.Error, "move or remove it, then restore again") {
		t.Fatalf("second restore: %d %+v", code, res)
	}
	if body, err := os.ReadFile(kept); err != nil || string(body) != "old" {
		t.Fatalf("the old data is gone: %q %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "b", "new.txt")); !os.IsNotExist(err) {
		t.Fatal("the refused restore changed the mount")
	}

	// The user clears it; folders without files and a killed run's
	// extraction folder do not stand in the way.
	if err := os.RemoveAll(filepath.Join(root, ".wf-restore-r4.old")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".wf-restore-z.old", "0", "old"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, root, ".wf-restore-q/x", "stale")
	if code, res := restore(t, tarOf(t, file("x/new.txt", "new")), args...); code != ExitOK {
		t.Fatalf("third restore: %d %+v", code, res)
	}
	if left, _ := filepath.Glob(filepath.Join(root, restoreTmpPrefix+"*")); len(left) != 0 {
		t.Fatalf("temp folders left: %v", left)
	}
}

// One folder listed twice as a whole mount (a volume mounted at two container
// paths) is restored once: the second target must not move the data back
// out.
func TestRestoreFilesTheSameMountListedTwice(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "f.txt", "original")
	archive := dumpOf(t, "--src", root, "--entry", "a", "--src", root, "--entry", "b")
	writeFileT(t, root, "f.txt", "newer")
	writeFileT(t, root, "g.txt", "later")
	recordChowns(t)
	code, res := restore(t, archive, "files", "--id", "d1", "--root", root, "--src", root, "--entry", "a", "--root", root, "--src", root, "--entry", "b")
	if code != ExitOK {
		t.Fatalf("%d %+v", code, res)
	}
	if body, err := os.ReadFile(filepath.Join(root, "f.txt")); err != nil || string(body) != "original" {
		t.Fatalf("f.txt = %q %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(root, "g.txt")); !os.IsNotExist(err) {
		t.Fatal("g.txt survived")
	}
}

// Excluded files are only kept inside a real folder: when the restored path
// is a symlink they stay out (and are reported), never moved through it.
func TestRestoreFilesKeepsNothingThroughASymlink(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	writeFileT(t, root, "media/sub/x.mp4", "movie")
	recordChowns(t)
	code, res := restore(t, tarOf(t, &tar.Header{Name: "app/media", Typeflag: tar.TypeSymlink, Linkname: other}), "files", "--id", "y1",
		"--root", root, "--src", root+"/media", "--entry", "app/media", "--exclude", "*.mp4")
	if code != ExitOK || len(res.Warnings) != 1 || res.Warnings[0] != "/app/media/sub/x.mp4: not kept, the restored copy is not a folder" {
		t.Fatalf("%d %+v", code, res)
	}
	if entries, _ := os.ReadDir(other); len(entries) != 0 {
		t.Fatalf("files were moved through the link: %v", entries)
	}
}

// A whole-volume dump leaves a restore's temp folders out of the copy.
func TestDumpSkipsRestoreTempFolders(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "data.txt", "d")
	writeFileT(t, root, restoreTmpPrefix+"x/big", "leftover")
	writeFileT(t, root, restoreTmpPrefix+"x.old/0/old", "old")
	for _, entry := range []string{"", "app"} {
		archive := dumpOf(t, "--src", root, "--entry", entry)
		var names []string
		for tr := tar.NewReader(bytes.NewReader(archive)); ; {
			h, err := tr.Next()
			if err != nil {
				break
			}
			names = append(names, h.Name)
		}
		for _, n := range names {
			if strings.Contains(n, restoreTmpPrefix+"x") {
				t.Errorf("entry %q: the archive holds %s", entry, n)
			}
		}
		if !slices.Contains(names, path.Join(entry, "data.txt")) {
			t.Errorf("entry %q: names = %v", entry, names)
		}
	}
}

// A files restore says it is about to swap (and only then): a helper killed
// before that line has changed nothing, and the agent starts the app again.
func TestRestoreFilesPrintsTheSwappingLineOnlyBeforeTheSwap(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "media/keep.txt", "original")
	args := func() []string {
		return []string{"restore", "files", "--id", "t1", "--root", root, "--src", root + "/media", "--entry", "app/media"}
	}
	var stderr bytes.Buffer
	good := tarOf(t, file("app/media/new.txt", "new"))
	recordChowns(t)
	if code := Run(args(), bytes.NewReader(good), io.Discard, &stderr); code != ExitOK {
		t.Fatalf("restore: %d\n%s", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) != 2 || lines[0] != helper.SwappingLine || !helper.Swapping(stderr.Bytes()) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	// Refused while extracting: no swap, no line.
	stderr.Reset()
	bad := tarOf(t, file("app/other/x", "x"))
	if code := Run(args(), bytes.NewReader(bad), io.Discard, &stderr); code != ExitFailed {
		t.Fatalf("refused restore: %d", code)
	}
	if helper.Swapping(stderr.Bytes()) {
		t.Fatalf("a restore refused before the swap printed the line: %q", stderr.String())
	}
}
