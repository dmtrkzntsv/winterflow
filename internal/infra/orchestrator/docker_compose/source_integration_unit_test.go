package dockercompose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
)

// sourcePayload builds a git-sourced app payload against a local upstream.
func sourcePayload(upstream string, composePath string) command.AppPayload {
	cfgSource := map[string]any{
		"repo_url":     upstream,
		"branch":       "master",
		"auto_update":  true,
		"poll_seconds": 1,
	}
	if composePath != "" {
		cfgSource["compose_path"] = composePath
	}
	cfg, _ := json.Marshal(map[string]any{
		"name":      "git app",
		"files":     []any{},
		"variables": []any{},
		"source":    cfgSource,
	})
	return command.AppPayload{
		AppID:  "git-app",
		Config: cfg,
		Source: &command.SourcePayload{
			RepoURL:     upstream,
			Branch:      "master",
			ComposePath: composePath,
			AutoUpdate:  true,
			PollSeconds: 1,
		},
	}
}

func TestSaveSourceAppPinsLockInCommit(t *testing.T) {
	r := newTestRepo(t)
	up, commit := newUpstream(t)
	c1 := commit("compose.yml", "services: {}\n")

	hash, err := r.saveWithoutDeploy(sourcePayload(up, ""))
	if err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("git-app")

	lock, ok := readSourceLock(dir)
	if !ok || lock.SHA != c1 {
		t.Fatalf("lock = %+v ok=%v want %s", lock, ok, c1)
	}
	// The lock must be part of the save commit: mutate it, restore the commit,
	// and expect the pinned value back.
	if err := writeSourceLock(dir, "0000000000000000000000000000000000000000"); err != nil {
		t.Fatal(err)
	}
	if err := gitRestore(dir, hash); err != nil {
		t.Fatal(err)
	}
	lock, _ = readSourceLock(dir)
	if lock.SHA != c1 {
		t.Fatalf("lock not committed with save: %+v", lock)
	}
	// source/ itself must NOT be tracked.
	if out, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); !strings.Contains(string(out), "source") {
		t.Fatalf(".gitignore misses source/: %q", out)
	}
}

func TestComposeFileResolution(t *testing.T) {
	r := newTestRepo(t)
	dir := filepath.Join(r.cfg.GetAppsDataDir(), "app-x")
	_ = os.MkdirAll(filepath.Join(dir, "source", "deploy"), 0o755)

	// compose_path explicitly set.
	writeFileT(t, dir, "source/deploy/stack.yml", "x")
	if got := r.composeFile(dir, &sourceSpec{ComposePath: "deploy/stack.yml"}); got != filepath.Join("source", "deploy", "stack.yml") {
		t.Fatalf("compose_path resolution = %q", got)
	}
	// Repo-root compose.yml auto-detected.
	writeFileT(t, dir, "source/compose.yml", "x")
	if got := r.composeFile(dir, &sourceSpec{}); got != filepath.Join("source", "compose.yml") {
		t.Fatalf("root compose.yml resolution = %q", got)
	}
	// docker-compose.yml fallback.
	_ = os.Remove(filepath.Join(dir, "source", "compose.yml"))
	writeFileT(t, dir, "source/docker-compose.yml", "x")
	if got := r.composeFile(dir, &sourceSpec{}); got != filepath.Join("source", "docker-compose.yml") {
		t.Fatalf("docker-compose.yml resolution = %q", got)
	}
	// Nothing in the repo: fall back to the winterflow-authored root file.
	_ = os.Remove(filepath.Join(dir, "source", "docker-compose.yml"))
	if got := r.composeFile(dir, &sourceSpec{}); got != "" {
		t.Fatalf("fallback should use auto-detect in app dir, got %q", got)
	}
	// Non-source apps never get -f.
	if got := r.composeFile(dir, nil); got != "" {
		t.Fatalf("non-source app resolution = %q", got)
	}
}

func TestComposeArgsAlwaysIncludeEnvFile(t *testing.T) {
	r := newTestRepo(t)
	savedApp(t, r, "app-1", "demo", "x\n")

	args := strings.Join(r.composeArgs("app-1", "up"), " ")
	if !strings.Contains(args, "--env-file .env") {
		t.Fatalf(".env must always be explicit now: %s", args)
	}
	if strings.Contains(args, ".env.secrets") {
		t.Fatalf("no secrets -> no secrets env file: %s", args)
	}
}

func TestSourceTokenStoredEncryptedAndMasked(t *testing.T) {
	r, pub := newSecretRepo(t)
	up, commit := newUpstream(t)
	commit("compose.yml", "services: {}\n")

	p := sourcePayload(up, "")
	p.Source.Token = []byte(eciesEncrypt(t, pub, "gh-token-plaintext"))
	if _, err := r.saveWithoutDeploy(p); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("git-app")

	raw, _ := os.ReadFile(filepath.Join(dir, secretsRel))
	if strings.Contains(string(raw), "gh-token-plaintext") {
		t.Fatal("token plaintext leaked into secrets.json")
	}
	var s secretStore
	_ = json.Unmarshal(raw, &s)
	if s.SourceToken == "" {
		t.Fatal("token ciphertext not stored")
	}
	if got := r.sourceTokenPlaintext(dir); got != "gh-token-plaintext" {
		t.Fatalf("decrypt round-trip = %q", got)
	}

	// GetApp masks the token.
	resp, err := r.GetApp(context.Background(), "git-app")
	if err != nil {
		t.Fatal(err)
	}
	if resp.App.Source == nil || string(resp.App.Source.Token) != command.EncryptedPlaceholder {
		t.Fatalf("GetApp source = %+v", resp.App.Source)
	}

	// Placeholder on re-save keeps the ciphertext.
	p2 := sourcePayload(up, "")
	p2.Source.Token = []byte(command.EncryptedPlaceholder)
	if _, err := r.saveWithoutDeploy(p2); err != nil {
		t.Fatal(err)
	}
	var s2 secretStore
	raw2, _ := os.ReadFile(filepath.Join(dir, secretsRel))
	_ = json.Unmarshal(raw2, &s2)
	if s2.SourceToken != s.SourceToken {
		t.Fatal("placeholder must preserve the token ciphertext")
	}
}

func TestRollbackRepinsSource(t *testing.T) {
	r := newTestRepo(t)
	up, commit := newUpstream(t)
	commit("marker.txt", "v1\n")

	h1, err := r.saveWithoutDeploy(sourcePayload(up, ""))
	if err != nil {
		t.Fatal(err)
	}

	// Upstream advances; a second save re-pins to the new head.
	commit("marker.txt", "v2\n")
	if _, err := r.saveWithoutDeploy(sourcePayload(up, "")); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("git-app")
	if got, _ := os.ReadFile(filepath.Join(dir, "source", "marker.txt")); string(got) != "v2\n" {
		t.Fatalf("pre-rollback source = %q", got)
	}

	// Rollback to the first save: the source checkout must return to v1.
	if _, err := r.rollbackWithoutDeploy("git-app", h1); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "source", "marker.txt")); string(got) != "v1\n" {
		t.Fatalf("post-rollback source = %q", got)
	}
}

func TestRefreshSourceWithoutDeploy(t *testing.T) {
	r := newTestRepo(t)
	up, commit := newUpstream(t)
	commit("f", "v1\n")
	if _, err := r.saveWithoutDeploy(sourcePayload(up, "")); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("git-app")
	before, _ := gitCount(dir)

	// No upstream change: no new commit.
	changed, _, err := r.refreshSourceWithoutDeploy("git-app")
	if err != nil || changed {
		t.Fatalf("no-change refresh: changed=%v err=%v", changed, err)
	}

	c2 := commit("f", "v2\n")
	changed, sha, err := r.refreshSourceWithoutDeploy("git-app")
	if err != nil || !changed || sha != c2 {
		t.Fatalf("refresh after advance: changed=%v sha=%s err=%v", changed, sha, err)
	}
	after, _ := gitCount(dir)
	if after != before+1 {
		t.Fatalf("refresh must commit the new pin: %d -> %d", before, after)
	}
}
