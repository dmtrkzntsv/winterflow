package sqlc

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A multibyte character anywhere in a query or migration file (comments
// included) can make sqlc emit a truncated SQL constant that fails only at
// runtime, so every file sqlc reads must be plain ASCII.
func TestSQLFilesAreASCII(t *testing.T) {
	n := 0
	for _, root := range []string{"query", filepath.Join("..", "migrations")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".sql") {
				return err
			}
			n++
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			line := 1
			for _, c := range b {
				if c == '\n' {
					line++
				}
				if c > 0x7F {
					t.Errorf("%s:%d: non-ASCII byte 0x%02X", path, line, c)
					return nil
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n == 0 {
		t.Fatal("no .sql files found: the test is looking in the wrong place")
	}
}
