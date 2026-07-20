package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"winterflow/internal/domain/command"
	"winterflow/pkg/crypto"
)

// Docker resource operations (registries + networks) via the `docker` CLI.
// v1 used the Docker SDK for networks; v2 stays CLI-only to match the rest of
// the orchestrator.

// --- registries ---------------------------------------------------------------

// ListRegistries returns the registries the agent is logged in to, read from
// the Docker config's `auths` section. A missing config means none.
func (r *Repository) ListRegistries(ctx context.Context) ([]command.Registry, error) {
	cfgPath, err := dockerConfigPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []command.Registry{}, nil
		}
		return nil, fmt.Errorf("read docker config: %w", err)
	}
	var cfg struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse docker config: %w", err)
	}
	out := make([]command.Registry, 0, len(cfg.Auths))
	for addr := range cfg.Auths {
		out = append(out, command.Registry{Address: addr})
	}
	return out, nil
}

// CreateRegistry logs in to a registry (`docker login`). The password is piped
// via stdin so it never appears in the process list; if the request marks it
// encrypted, it is decrypted with the agent key first.
func (r *Repository) CreateRegistry(ctx context.Context, in command.CreateRegistryRequest) error {
	if in.Address == "" || in.Username == "" {
		return fmt.Errorf("address and username are required")
	}
	password := in.Password
	if in.Encrypted && password != "" {
		dec, err := crypto.DecryptWithPrivateKey(r.cfg.GetAgentKeyPath(), password)
		if err != nil {
			return fmt.Errorf("decrypt registry password: %w", err)
		}
		password = dec
	}

	cmd := exec.CommandContext(ctx, "docker", "login", in.Address, "--username", in.Username, "--password-stdin")
	cmd.Stdin = strings.NewReader(password)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker login: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DeleteRegistry logs out of a registry (`docker logout`).
func (r *Repository) DeleteRegistry(ctx context.Context, address string) error {
	if address == "" {
		return fmt.Errorf("address is required")
	}
	cmd := exec.CommandContext(ctx, "docker", "logout", address)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker logout: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// --- networks -----------------------------------------------------------------

// dockerNetwork mirrors one line of `docker network ls --format json`.
type dockerNetwork struct {
	ID     string `json:"ID"`
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
	Scope  string `json:"Scope"`
}

// ListNetworks returns the Docker networks on the agent.
func (r *Repository) ListNetworks(ctx context.Context) ([]command.Network, error) {
	cmd := exec.CommandContext(ctx, "docker", "network", "ls", "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker network ls: %w", err)
	}
	networks := make([]command.Network, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var dn dockerNetwork
		if err := json.Unmarshal([]byte(line), &dn); err != nil {
			r.log.Warn("parse network line", "error", err)
			continue
		}
		networks = append(networks, command.Network{ID: dn.ID, Name: dn.Name, Driver: dn.Driver, Scope: dn.Scope})
	}
	return networks, nil
}

// CreateNetwork creates a Docker network.
func (r *Repository) CreateNetwork(ctx context.Context, in command.CreateNetworkRequest) error {
	if in.Name == "" {
		return fmt.Errorf("network name is required")
	}
	args := []string{"network", "create"}
	if in.Driver != "" {
		args = append(args, "--driver", in.Driver)
	}
	args = append(args, in.Name)
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker network create: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DeleteNetwork removes a Docker network.
func (r *Repository) DeleteNetwork(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("network name is required")
	}
	cmd := exec.CommandContext(ctx, "docker", "network", "rm", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker network rm: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// dockerConfigPath resolves ~/.docker/config.json, honoring DOCKER_CONFIG.
func dockerConfigPath() (string, error) {
	dir := os.Getenv("DOCKER_CONFIG")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		dir = filepath.Join(home, ".docker")
	}
	return filepath.Join(dir, "config.json"), nil
}
