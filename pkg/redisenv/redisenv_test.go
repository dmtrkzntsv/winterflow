package redisenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "redis.env")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, "hub:hub-01.example.com", "abc-DEF_123"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "REDIS_USERNAME=hub:hub-01.example.com\nREDIS_PASSWORD=abc-DEF_123\n" {
		t.Fatalf("content = %q", data)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestWriteRefuses(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct{ path, user, pass string }{
		{"", "u", "p"},
		{filepath.Join(dir, "a"), "", "p"},
		{filepath.Join(dir, "a"), "u", ""},
		{filepath.Join(dir, "a"), "u", "p\nX=1"},
		{filepath.Join(dir, "a"), "u", "p$x"},
		{filepath.Join(dir, "missing", "a"), "u", "p"},
	} {
		if err := Write(c.path, c.user, c.pass); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); !os.IsNotExist(err) {
		t.Fatalf("a refused write left a file: %v", err)
	}
}
