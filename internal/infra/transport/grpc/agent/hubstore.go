package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// HubStore keeps the hub the API last moved this server to in
// {AGENT_DATA_DIR}/hub.json; while it holds one, the agent dials it instead
// of HUB_HOST/HUB_PORT.
type HubStore struct {
	path string

	mu sync.Mutex
	// host/port is the last hub saved by this process: a move still takes
	// effect when the file cannot be written.
	host, port string
}

type hubFile struct {
	Host string `json:"host"`
	Port string `json:"port"`
}

func NewHubStore(dataDir string) *HubStore {
	return &HubStore{path: filepath.Join(dataDir, "hub.json")}
}

// Load returns the stored hub; ok is false when there is none (no file, or
// one that does not read as a host and a port).
func (s *HubStore) Load() (host, port string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.host != "" {
		return s.host, s.port, true
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return "", "", false
	}
	var f hubFile
	if json.Unmarshal(raw, &f) != nil || f.Host == "" || f.Port == "" {
		return "", "", false
	}
	return f.Host, f.Port, true
}

// Save records the hub: in memory at once, and in hub.json through a temp
// file beside it (0600) renamed over the old one, so a reader never sees a
// half-written file.
func (s *HubStore) Save(host, port string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.host, s.port = host, port
	raw, err := json.Marshal(hubFile{Host: host, Port: port})
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".hub.json.*")
	if err != nil {
		return fmt.Errorf("write hub.json: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name) // after a successful rename there is nothing left to remove
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("write hub.json: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write hub.json: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("write hub.json: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write hub.json: %w", err)
	}
	if err := os.Rename(name, s.path); err != nil {
		return fmt.Errorf("write hub.json: %w", err)
	}
	return nil
}
