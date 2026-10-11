package dockercompose

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
)

func TestWriteAppStoreEnvOmitsProjectName(t *testing.T) {
	r, pub := newSecretRepo(t)
	dir := filepath.Join(r.cfg.GetAppsDataDir(), "app-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// The project name travels as --project-name only; a user variable with
	// the reserved name is dropped rather than written.
	p := storePayload(pub, t)
	p.Variables = append(p.Variables, command.ContentItem{Name: "COMPOSE_PROJECT_NAME", Content: []byte("sneaky")})
	if _, err := r.writeAppStore(dir, p); err != nil {
		t.Fatal(err)
	}

	env, _ := os.ReadFile(filepath.Join(dir, envRel))
	if strings.Contains(string(env), "COMPOSE_PROJECT_NAME") {
		t.Fatalf(".env must not carry COMPOSE_PROJECT_NAME: %q", env)
	}
	// The manual-run helper is derived state, not history.
	gi, _ := os.ReadFile(filepath.Join(dir, gitignoreRel))
	if !strings.Contains(string(gi), runRel) {
		t.Fatalf(".gitignore missing %s: %q", runRel, gi)
	}
}

func TestGetAppHidesManagedProjectName(t *testing.T) {
	r, pub := newSecretRepo(t)
	dir := filepath.Join(r.cfg.GetAppsDataDir(), "app-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.writeAppStore(dir, storePayload(pub, t)); err != nil {
		t.Fatal(err)
	}
	// Simulate a .env written by an older agent that still managed the name.
	env := "COMPOSE_PROJECT_NAME=wf-app-1\nPORT=8080\n"
	if err := os.WriteFile(filepath.Join(dir, envRel), []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}

	resp, err := r.GetApp(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, v := range resp.App.Variables {
		names[v.Name] = true
	}
	if names["COMPOSE_PROJECT_NAME"] {
		t.Fatal("managed COMPOSE_PROJECT_NAME leaked into editor variables")
	}
	if !names["PORT"] {
		t.Fatalf("user variable lost: %v", names)
	}
}

func TestRunHelperScript(t *testing.T) {
	got := string(runHelperScript("wf-app-1", []string{"compose.yml", "compose.override.yml"}))
	for _, want := range []string{
		"#!/bin/sh\n",
		`cd "$(dirname "$0")/.." || exit 1`,
		`if [ -f .env.secrets ]; then set -- --env-file .env.secrets "$@"; fi`,
		`if [ -f .winterflow/storage.env ]; then set -- --env-file .winterflow/storage.env "$@"; fi`,
		`exec docker compose --project-name wf-app-1 -f 'compose.yml' -f 'compose.override.yml' --env-file .env "$@"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("script missing %q:\n%s", want, got)
		}
	}
	// The env files are prepended last-first, so compose sees .env,
	// storage.env, .env.secrets in that order (later ones win).
	if strings.Index(got, ".env.secrets \"$@\"; fi") > strings.Index(got, "storage.env \"$@\"; fi") {
		t.Fatalf("storage.env must be prepended after .env.secrets:\n%s", got)
	}

	// A compose path is quoted for the shell, whatever it contains.
	odd := string(runHelperScript("wf-app-1", []string{"source/it's $(here).yml"}))
	if !strings.Contains(odd, `-f 'source/it'\''s $(here).yml' --env-file .env`) {
		t.Fatalf("path not quoted:\n%s", odd)
	}
}

// TestRunHelperScriptRuns executes the script with a fake docker on PATH:
// it must pass exactly winterflow's flags, env files included.
func TestRunHelperScriptRuns(t *testing.T) {
	dir := t.TempDir()
	bin := t.TempDir()
	writeFileT(t, bin, "docker", "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done\n")
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, dir, runRel, string(runHelperScript("wf-x", []string{"compose.yml"})))
	if err := os.Chmod(filepath.Join(dir, runRel), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func() string {
		cmd := exec.Command(filepath.Join(dir, runRel), "up", "-d")
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run.sh: %v: %s", err, out)
		}
		return strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", " ")
	}
	if got := run(); got != "compose --project-name wf-x -f compose.yml --env-file .env up -d" {
		t.Fatalf("without extra env files: %s", got)
	}
	writeFileT(t, dir, envSecretsRel, "")
	writeFileT(t, dir, storageEnvRel, "")
	if got := run(); got != "compose --project-name wf-x -f compose.yml --env-file .env --env-file .winterflow/storage.env --env-file .env.secrets up -d" {
		t.Fatalf("with env files: %s", got)
	}
}

func TestWriteRunHelper(t *testing.T) {
	r, pub := newSecretRepo(t)
	dir := filepath.Join(r.cfg.GetAppsDataDir(), "app-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.writeAppStore(dir, storePayload(pub, t)); err != nil {
		t.Fatal(err)
	}

	if err := r.writeRunHelper("app-1"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, runRel))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("run helper not executable: %v", info.Mode())
	}
	raw, _ := os.ReadFile(filepath.Join(dir, runRel))
	if !strings.Contains(string(raw), "--project-name wf-app-1") {
		t.Fatalf("helper content wrong:\n%s", raw)
	}
}
