package dockercompose

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFileT(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGitEnsureCommitCount(t *testing.T) {
	dir := t.TempDir()
	if err := gitEnsure(dir); err != nil {
		t.Fatal(err)
	}
	// Idempotent on an existing repo.
	if err := gitEnsure(dir); err != nil {
		t.Fatal(err)
	}

	writeFileT(t, dir, "compose.yml", "services: {}\n")
	h1, err := gitCommitAll(dir, "save one")
	if err != nil {
		t.Fatal(err)
	}
	if h1 == "" {
		t.Fatal("empty hash")
	}
	if n, _ := gitCount(dir); n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}

	writeFileT(t, dir, "compose.yml", "services:\n  app: {}\n")
	h2, err := gitCommitAll(dir, "save two")
	if err != nil {
		t.Fatal(err)
	}
	if h2 == h1 {
		t.Fatal("second commit should have a new hash")
	}
	if n, _ := gitCount(dir); n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
}

func TestGitCommitAllCleanWorktreeIsNoop(t *testing.T) {
	dir := t.TempDir()
	if err := gitEnsure(dir); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, dir, "a.txt", "x")
	h1, err := gitCommitAll(dir, "first")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := gitCommitAll(dir, "no changes")
	if err != nil {
		t.Fatal(err)
	}
	if h2 != h1 {
		t.Fatalf("clean worktree must not create a commit: %s != %s", h2, h1)
	}
	if n, _ := gitCount(dir); n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
}

func TestGitIgnoredFilesStayOut(t *testing.T) {
	dir := t.TempDir()
	if err := gitEnsure(dir); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, dir, ".gitignore", ".env.secrets\n")
	writeFileT(t, dir, ".env.secrets", "DB_PASS=plaintext\n")
	writeFileT(t, dir, "compose.yml", "services: {}\n")
	if _, err := gitCommitAll(dir, "save"); err != nil {
		t.Fatal(err)
	}

	// Committing again with only the ignored file changed must be a no-op.
	h1, _ := gitCommitAll(dir, "noop")
	writeFileT(t, dir, ".env.secrets", "DB_PASS=changed\n")
	h2, err := gitCommitAll(dir, "still noop")
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatal("ignored file change produced a commit")
	}
}

func TestGitLogNewestFirst(t *testing.T) {
	dir := t.TempDir()
	_ = gitEnsure(dir)
	writeFileT(t, dir, "f", "1")
	h1, _ := gitCommitAll(dir, "one")
	writeFileT(t, dir, "f", "2")
	h2, _ := gitCommitAll(dir, "two")

	log, err := gitLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 {
		t.Fatalf("log len = %d", len(log))
	}
	if log[0].Hash != h2 || log[1].Hash != h1 {
		t.Fatalf("order wrong: %+v", log)
	}
	if log[0].Subject != "two" || log[1].Subject != "one" {
		t.Fatalf("subjects wrong: %+v", log)
	}
	now := time.Now().Unix()
	if log[0].Timestamp == 0 || log[0].Timestamp > now+60 {
		t.Fatalf("timestamp implausible: %d", log[0].Timestamp)
	}
}

func TestGitRestoreBringsBackOldTree(t *testing.T) {
	dir := t.TempDir()
	_ = gitEnsure(dir)
	writeFileT(t, dir, ".gitignore", ".env.secrets\n")
	writeFileT(t, dir, "compose.yml", "version-one\n")
	writeFileT(t, dir, "sub/keep.txt", "keep\n")
	h1, _ := gitCommitAll(dir, "one")

	// Second commit: modify, delete, add — and put an ignored file on disk.
	writeFileT(t, dir, "compose.yml", "version-two\n")
	if err := os.Remove(filepath.Join(dir, "sub", "keep.txt")); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, dir, "added-later.txt", "later\n")
	writeFileT(t, dir, ".env.secrets", "SECRET=1\n")
	if _, err := gitCommitAll(dir, "two"); err != nil {
		t.Fatal(err)
	}

	if err := gitRestore(dir, h1); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(filepath.Join(dir, "compose.yml")); string(got) != "version-one\n" {
		t.Fatalf("compose.yml = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "sub", "keep.txt")); string(got) != "keep\n" {
		t.Fatalf("deleted file not restored: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "added-later.txt")); !os.IsNotExist(err) {
		t.Fatal("file added after h1 should be removed by restore")
	}
	// Ignored (unmanaged) files are left alone.
	if got, _ := os.ReadFile(filepath.Join(dir, ".env.secrets")); string(got) != "SECRET=1\n" {
		t.Fatalf("ignored file touched by restore: %q", got)
	}

	// Restore does not commit by itself; committing the restored tree works.
	h3, err := gitCommitAll(dir, "rollback to one")
	if err != nil {
		t.Fatal(err)
	}
	if h3 == h1 {
		t.Fatal("rollback must be a NEW commit")
	}
	if n, _ := gitCount(dir); n != 3 {
		t.Fatalf("count = %d, want 3 (linear history)", n)
	}
}

func TestGitRestoreUnknownHashErrors(t *testing.T) {
	dir := t.TempDir()
	_ = gitEnsure(dir)
	writeFileT(t, dir, "f", "1")
	_, _ = gitCommitAll(dir, "one")
	if err := gitRestore(dir, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"); err == nil {
		t.Fatal("expected error for unknown hash")
	}
}
