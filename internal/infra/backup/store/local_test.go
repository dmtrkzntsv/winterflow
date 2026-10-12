package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
		if strings.HasPrefix(filepath.Base(p), tmpPrefix) {
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
	if err := os.WriteFile(filepath.Join(root, tmpPrefix+"half"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.List(context.Background(), ""); len(got) != 1 || got[0].Key != "app-1/db/20261011T000000Z-manual.db.zst" {
		t.Fatalf("list = %+v", got)
	}
}

func TestOpenLocalNeedsAnExistingFolder(t *testing.T) {
	root := t.TempDir()
	if _, err := openLocal(filepath.Join(root, "usb")); !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), "is the disk mounted") {
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

// A disk unplugged after Open leaves its mount point behind: the driver must
// refuse (for a folder on the disk) instead of recreating the folder on the
// system disk.
func TestLocalStoreRootVanished(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "usb")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := openLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	missing := fmt.Sprintf("folder %s is missing", root)
	if err := st.Put(ctx, "app-1/db/x.db.zst", strings.NewReader("x")); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("put: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("Put recreated the root: %v", err)
	}
	if _, err := st.Get(ctx, "app-1/db/x.db.zst"); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("get: %v", err)
	}
	if _, err := st.List(ctx, ""); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("list: %v", err)
	}
	if err := st.Delete(ctx, "app-1/db/x.db.zst"); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("delete: %v", err)
	}
	// A file where the folder was is no folder either.
	if err := os.WriteFile(root, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.Put(ctx, "x", strings.NewReader("x")); err == nil || !strings.Contains(err.Error(), "is not a folder") {
		t.Fatalf("put over a file: %v", err)
	}
}

// Folders below the root are still created on demand.
func TestLocalStoreCreatesFoldersBelowTheRoot(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := openLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Put(ctx, "a/b/c/d.zst", strings.NewReader("deep")); err != nil {
		t.Fatal(err)
	}
	// A file in the way of a folder is refused, not replaced.
	if err := st.Put(ctx, "a/b/c/d.zst/e", strings.NewReader("x")); err == nil {
		t.Fatal("put under a file succeeded")
	}
	rc, err := st.Get(ctx, "a/b/c/d.zst")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if b, _ := io.ReadAll(rc); string(b) != "deep" {
		t.Fatalf("got %q", b)
	}
}

// Concurrent Puts of one key each write their own temp file: both succeed
// and the object is one of them, whole.
func TestLocalStoreConcurrentPuts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := openLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string]bool{}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		body := strings.Repeat(fmt.Sprintf("writer-%d;", i), 50000)
		bodies[body] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- st.Put(ctx, "app/db/x.zst", strings.NewReader(body))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("put: %v", err)
		}
	}
	rc, err := st.Get(ctx, "app/db/x.zst")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bodies[string(got)] {
		t.Fatalf("the object is no single writer's body (%d bytes)", len(got))
	}
	entries, _ := os.ReadDir(filepath.Join(root, "app", "db"))
	if len(entries) != 1 {
		t.Fatalf("left behind: %v", entries)
	}
}

// A real key ending in .tmp is an object like any other.
func TestLocalStoreTmpSuffixedKey(t *testing.T) {
	ctx := context.Background()
	st, err := openLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Put(ctx, "x.tmp", strings.NewReader("real")); err != nil {
		t.Fatal(err)
	}
	rc, err := st.Get(ctx, "x.tmp")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if b, _ := io.ReadAll(rc); string(b) != "real" {
		t.Fatalf("got %q", b)
	}
	if list, err := st.List(ctx, ""); err != nil || len(list) != 1 || list[0].Key != "x.tmp" {
		t.Fatalf("list = %+v, %v", list, err)
	}
}
