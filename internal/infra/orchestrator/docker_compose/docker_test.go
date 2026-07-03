package dockercompose

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"winterflow/internal/domain/command"
)

func TestDockerConfigPath(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", "/custom/docker")
	got, err := dockerConfigPath()
	if err != nil {
		t.Fatalf("dockerConfigPath: %v", err)
	}
	if want := filepath.Join("/custom/docker", "config.json"); got != want {
		t.Errorf("with DOCKER_CONFIG: %q, want %q", got, want)
	}

	home := t.TempDir()
	t.Setenv("DOCKER_CONFIG", "")
	t.Setenv("HOME", home)
	got, err = dockerConfigPath()
	if err != nil {
		t.Fatalf("dockerConfigPath (home): %v", err)
	}
	if want := filepath.Join(home, ".docker", "config.json"); got != want {
		t.Errorf("with HOME fallback: %q, want %q", got, want)
	}

	// No DOCKER_CONFIG and no resolvable home dir is an error.
	t.Setenv("HOME", "")
	if _, err := dockerConfigPath(); err == nil {
		t.Error("expected error when home dir cannot be resolved")
	}
}

func TestListRegistries(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)

	// Missing config file means no registries, not an error.
	regs, err := r.ListRegistries(ctx)
	if err != nil {
		t.Fatalf("ListRegistries (missing config): %v", err)
	}
	if len(regs) != 0 {
		t.Errorf("registries = %v, want empty", regs)
	}

	cfg := `{"auths":{"ghcr.io":{"auth":"eDp5"},"registry.example.com":{}}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	regs, err = r.ListRegistries(ctx)
	if err != nil {
		t.Fatalf("ListRegistries: %v", err)
	}
	got := map[string]bool{}
	for _, reg := range regs {
		got[reg.Address] = true
	}
	if len(regs) != 2 || !got["ghcr.io"] || !got["registry.example.com"] {
		t.Errorf("registries = %v, want ghcr.io + registry.example.com", regs)
	}

	// Malformed config is an error.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ListRegistries(ctx); err == nil {
		t.Error("expected error for malformed docker config")
	}

	// An unreadable config (a directory in its place) is an error, not "no
	// registries".
	dir2 := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir2)
	if err := os.MkdirAll(filepath.Join(dir2, "config.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ListRegistries(ctx); err == nil {
		t.Error("expected error when docker config cannot be read")
	}
}

func TestCreateRegistryValidation(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	if err := r.CreateRegistry(ctx, command.CreateRegistryRequest{Username: "u"}); err == nil {
		t.Error("expected error for missing address")
	}
	if err := r.CreateRegistry(ctx, command.CreateRegistryRequest{Address: "ghcr.io"}); err == nil {
		t.Error("expected error for missing username")
	}

	// An encrypted password that cannot be decrypted must fail before any
	// docker login attempt.
	err := r.CreateRegistry(ctx, command.CreateRegistryRequest{
		Address:   "ghcr.io",
		Username:  "u",
		Password:  "not-a-valid-ecies-payload",
		Encrypted: true,
	})
	if err == nil {
		t.Error("expected decrypt error for bogus encrypted password")
	}
}

func TestDeleteRegistryRequiresAddress(t *testing.T) {
	r := newTestRepo(t)
	if err := r.DeleteRegistry(context.Background(), ""); err == nil {
		t.Error("expected error for empty address")
	}
}

func TestCreateNetworkRequiresName(t *testing.T) {
	r := newTestRepo(t)
	if err := r.CreateNetwork(context.Background(), command.CreateNetworkRequest{}); err == nil {
		t.Error("expected error for empty network name")
	}
}

func TestDeleteNetworkRequiresName(t *testing.T) {
	r := newTestRepo(t)
	if err := r.DeleteNetwork(context.Background(), ""); err == nil {
		t.Error("expected error for empty network name")
	}
}
