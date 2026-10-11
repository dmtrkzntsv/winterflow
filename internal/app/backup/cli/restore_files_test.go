package cli

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
// in the outer volume, then the inner volume's root). Restore takes both
// and keeps the mount point in the outer volume.
func TestRestoreFilesNestedMount(t *testing.T) {
	root := t.TempDir()
	outer, inner := filepath.Join(root, "0"), filepath.Join(root, "1")
	for _, f := range []struct{ path, body string }{
		{"0/app.yml", "k: v"}, {"0/sub/notes.txt", "notes"}, {"1/photo.jpg", "jpeg"}, {"1/2026/a.jpg", "a"},
	} {
		writeFileT(t, root, f.path, f.body)
	}
	if err := os.Mkdir(filepath.Join(outer, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := dumpOf(t, "--src", outer, "--entry", "data", "--src", inner, "--entry", "data/media")
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
	recordChowns(t)
	code, res := restore(t, archive, "files", "--id", "n1",
		"--root", outer, "--src", outer, "--entry", "data",
		"--root", inner, "--src", inner, "--entry", "data/media")
	if code != ExitOK || len(res.Warnings) != 0 {
		t.Fatalf("%d %+v", code, res)
	}
	if after := snapshot(t, outer); !mapsEqual(after, beforeOuter) {
		t.Fatalf("outer:\n%v\nwant\n%v", after, beforeOuter)
	}
	if after := snapshot(t, inner); !mapsEqual(after, beforeInner) {
		t.Fatalf("inner:\n%v\nwant\n%v", after, beforeInner)
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

// A failure while swapping (here: the old path can't be removed) reports a
// partial restore.
func TestRestoreFilesPartialSwap(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores folder permissions")
	}
	root := t.TempDir()
	writeFileT(t, root, "a/b/old.txt", "old")
	if err := os.Chmod(filepath.Join(root, "a"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "a"), 0o755) })
	recordChowns(t)
	code, res := restore(t, tarOf(t, file("x/new.txt", "new")), "files", "--id", "r4",
		"--root", root, "--src", root+"/a/b", "--entry", "x")
	if code != ExitPartial || !strings.Contains(res.Error, "/x was partly replaced") {
		t.Fatalf("%d %+v", code, res)
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
