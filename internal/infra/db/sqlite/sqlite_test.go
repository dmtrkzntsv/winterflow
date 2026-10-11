package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
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
		"cache_size":   "1000",
		"mmap_size":    "268435456",
	} {
		if got, err := pragmaValue(db, pragma); err != nil || got != want {
			t.Errorf("PRAGMA %s = %q, %v; want %q", pragma, got, err, want)
		}
	}
	if s := db.Stats(); s.MaxOpenConnections != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", s.MaxOpenConnections)
	}
}

func pragmaValue(db *sql.DB, pragma string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var got string
	err := db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got)
	return got, err
}

// database/sql throws away a connection the driver reports unusable (modernc
// does after a statement is interrupted by its context) and opens a new one;
// the pragmas must come back with it, foreign keys above all.
func TestPragmasSurviveAConnectionReplacedAfterInterrupt(t *testing.T) {
	db, err := Open(context.Background(), Scheme+filepath.Join(t.TempDir(), "wf.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var n int
	err = db.QueryRowContext(ctx, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 1000000000) SELECT count(*) FROM c`).Scan(&n)
	if err == nil {
		t.Fatal("endless query was not interrupted")
	}

	for pragma, want := range map[string]string{
		"foreign_keys": "1",
		"synchronous":  "1",
		"temp_store":   "2",
		"cache_size":   "1000",
		"mmap_size":    "268435456",
	} {
		if got, err := pragmaValue(db, pragma); err != nil || got != want {
			t.Errorf("after interrupt: PRAGMA %s = %q, %v; want %q", pragma, got, err, want)
		}
	}
}

// A path with URI metacharacters is a path, not a query or a fragment.
func TestOpenPathWithURIMetacharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a?b#c%d", "wf.sqlite")
	db, err := Open(context.Background(), Scheme+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database not created at the literal path: %v", err)
	}
	if got, err := pragmaValue(db, "foreign_keys"); err != nil || got != "1" {
		t.Fatalf("foreign_keys = %q, %v", got, err)
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
