package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalStore(t *testing.T) {
	root := t.TempDir()
	st, err := openLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, st)

	// Folders 0700, files 0600, no temp files left.
	info, err := os.Stat(filepath.Join(root, "app-2", "files"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, %v", info, err)
	}
	info, err = os.Stat(filepath.Join(root, "app-2", "files", "x.tar.zst"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, %v", info, err)
	}
	_ = filepath.WalkDir(root, func(p string, _ os.DirEntry, _ error) error {
		if strings.HasSuffix(p, tmpSuffix) {
			t.Errorf("temp file left: %s", p)
		}
		return nil
	})
	// Delete prunes the folders it empties, never the root.
	if err := st.Delete(context.Background(), "app-2/files/x.tar.zst"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "app-2")); !os.IsNotExist(err) {
		t.Fatalf("empty folder kept: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("root removed: %v", err)
	}
	// An interrupted Put's temp file is not an object.
	if err := os.WriteFile(filepath.Join(root, "half.tmp"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.List(context.Background(), ""); len(got) != 1 || got[0].Key != "app-1/db/20261011T000000Z-manual.db.zst" {
		t.Fatalf("list = %+v", got)
	}
}

func TestOpenLocalNeedsAnExistingFolder(t *testing.T) {
	root := t.TempDir()
	if _, err := openLocal(filepath.Join(root, "usb")); err == nil || !strings.Contains(err.Error(), "is the disk mounted") {
		t.Fatalf("missing folder: %v", err)
	}
	file := filepath.Join(root, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openLocal(file); err == nil || !strings.Contains(err.Error(), "is not a folder") {
		t.Fatalf("a file: %v", err)
	}
	// Nothing was created on the way.
	if _, err := os.Stat(filepath.Join(root, "usb")); !os.IsNotExist(err) {
		t.Fatal("openLocal created the folder")
	}
}
