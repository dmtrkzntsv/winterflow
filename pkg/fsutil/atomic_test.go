package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.json")
	if err := WriteFile(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(raw) != "two" || info.Mode().Perm() != 0o600 {
		t.Fatalf("content %q, mode %v", raw, info.Mode())
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file left behind")
	}
	// A failed write names the file and leaves no temp file.
	err := WriteFile(filepath.Join(dir, "missing", "b.json"), []byte("x"), 0o600)
	if err == nil || !strings.HasPrefix(err.Error(), "write b.json: ") {
		t.Fatalf("err = %v", err)
	}
}

type failing struct{}

func (failing) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestWriteReader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obj")
	if err := WriteReader(path, ".tmp-*", strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "data" {
		t.Fatalf("content %q", raw)
	}
	// A reader that fails leaves the old content and no temp file.
	if err := WriteReader(path, ".tmp-*", failing{}); err == nil {
		t.Fatal("a failing reader must fail the write")
	}
	if raw, _ := os.ReadFile(path); string(raw) != "data" {
		t.Fatalf("content %q", raw)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("left behind: %v", entries)
	}
	if err := WriteReader(filepath.Join(dir, "missing", "obj"), ".tmp-*", strings.NewReader("x")); err == nil {
		t.Fatal("a missing folder must fail the write")
	}
	SyncDir(filepath.Join(dir, "missing")) // best effort: no panic
}
