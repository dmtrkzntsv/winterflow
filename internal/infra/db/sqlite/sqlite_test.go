package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAppliesPragmasAndCreatesDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "wf.sqlite")
	db, err := Open(context.Background(), Scheme+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	for pragma, want := range map[string]string{
		"foreign_keys": "1",
		"journal_mode": "wal",
		"synchronous":  "1",
		"temp_store":   "2",
	} {
		var got string
		if err := db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
			t.Errorf("PRAGMA %s = %q, %v; want %q", pragma, got, err, want)
		}
	}
	if s := db.Stats(); s.MaxOpenConnections != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", s.MaxOpenConnections)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(context.Background(), Scheme); err == nil {
		t.Fatal("empty path accepted")
	}
}

func TestOpenFailsOnUnwritableDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), Scheme+filepath.Join(file, "wf.sqlite")); err == nil {
		t.Fatal("Open succeeded under a regular file")
	}
}
