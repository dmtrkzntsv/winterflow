package dockercompose

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// newUpstream creates a local git repo acting as the remote, returning its
// path and a commit function that writes a file and commits.
func newUpstream(t *testing.T) (string, func(rel, content string) string) {
	t.Helper()
	up := t.TempDir()
	if err := gitEnsure(up); err != nil {
		t.Fatal(err)
	}
	commit := func(rel, content string) string {
		writeFileT(t, up, rel, content)
		h, err := gitCommitAll(up, "upstream: "+rel)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	return up, commit
}

func TestSourceFromConfig(t *testing.T) {
	if got := sourceFromConfig(map[string]any{"name": "x"}); got != nil {
		t.Fatalf("no source key -> nil, got %+v", got)
	}
	cfg := map[string]any{
		"source": map[string]any{
			"repo_url":     "https://example.com/repo.git",
			"branch":       "main",
			"compose_path": "deploy/compose.yml",
			"auto_update":  true,
			"poll_seconds": float64(60), // JSON numbers decode as float64
		},
	}
	got := sourceFromConfig(cfg)
	if got == nil || got.RepoURL != "https://example.com/repo.git" || got.Branch != "main" ||
		got.ComposePath != "deploy/compose.yml" || !got.AutoUpdate || got.PollSeconds != 60 {
		t.Fatalf("sourceFromConfig = %+v", got)
	}
}

func TestEnsureSourceClonesAndFollowsHead(t *testing.T) {
	r := newTestRepo(t)
	up, commit := newUpstream(t)
	c1 := commit("compose.yml", "one\n")

	dir := filepath.Join(r.cfg.GetAppsDataDir(), "app-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := sourceSpec{RepoURL: up, Branch: "master"}

	sha, err := r.ensureSource(context.Background(), dir, spec, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if sha != c1 {
		t.Fatalf("sha = %s, want %s", sha, c1)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "source", "compose.yml")); string(got) != "one\n" {
		t.Fatalf("source content = %q", got)
	}
	lock, ok := readSourceLock(dir)
	if !ok || lock.SHA != c1 {
		t.Fatalf("lock = %+v ok=%v", lock, ok)
	}

	// No upstream change: same SHA.
	again, err := r.ensureSource(context.Background(), dir, spec, "", "")
	if err != nil || again != c1 {
		t.Fatalf("second ensure = %s, %v", again, err)
	}

	// Upstream advances: follow head.
	c2 := commit("compose.yml", "two\n")
	sha2, err := r.ensureSource(context.Background(), dir, spec, "", "")
	if err != nil || sha2 != c2 {
		t.Fatalf("after advance = %s, %v (want %s)", sha2, err, c2)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "source", "compose.yml")); string(got) != "two\n" {
		t.Fatalf("source content after advance = %q", got)
	}
}

func TestEnsureSourcePinWinsOverHead(t *testing.T) {
	r := newTestRepo(t)
	up, commit := newUpstream(t)
	c1 := commit("f", "v1\n")
	commit("f", "v2\n") // head moves on

	dir := filepath.Join(r.cfg.GetAppsDataDir(), "app-1")
	_ = os.MkdirAll(dir, 0o755)
	spec := sourceSpec{RepoURL: up, Branch: "master"}

	sha, err := r.ensureSource(context.Background(), dir, spec, "", c1)
	if err != nil {
		t.Fatal(err)
	}
	if sha != c1 {
		t.Fatalf("pinned sha = %s, want %s", sha, c1)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "source", "f")); string(got) != "v1\n" {
		t.Fatalf("pinned content = %q", got)
	}
}

func TestEnsureSourceMissingBranchErrors(t *testing.T) {
	r := newTestRepo(t)
	up, commit := newUpstream(t)
	commit("f", "x\n")

	dir := filepath.Join(r.cfg.GetAppsDataDir(), "app-1")
	_ = os.MkdirAll(dir, 0o755)
	if _, err := r.ensureSource(context.Background(), dir, sourceSpec{RepoURL: up, Branch: "nope"}, "", ""); err == nil {
		t.Fatal("expected error for missing branch")
	}
}
