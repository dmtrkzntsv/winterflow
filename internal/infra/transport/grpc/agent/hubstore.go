package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// HubStore keeps the hub the API last named for this server in
// {AGENT_DATA_DIR}/hub.json; while it holds one, the agent dials it instead
// of HUB_HOST/HUB_PORT. Next to the address it keeps the fingerprints of the
// hub certificates the API trusts (the pin set; empty when the API named
// none or the file predates them).
type HubStore struct {
	path string

	mu sync.Mutex
	// host/port/fps is the last hub saved by this process: a move still
	// takes effect when the file cannot be written.
	host, port string
	fps        []string
}

type hubFile struct {
	Host         string   `json:"host"`
	Port         string   `json:"port"`
	Fingerprints []string `json:"fingerprints"`
}

func NewHubStore(dataDir string) *HubStore {
	return &HubStore{path: filepath.Join(dataDir, "hub.json")}
}

// Load returns the stored hub and its pin set (lower-case hex, possibly
// empty: a file without "fingerprints" reads as an empty set); ok is false
// when there is none (no file, or one that does not read as a host and a
// port), and fps is then empty too.
func (s *HubStore) Load() (host, port string, fps []string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.host != "" {
		return s.host, s.port, slices.Clone(s.fps), true
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return "", "", nil, false
	}
	var f hubFile
	if json.Unmarshal(raw, &f) != nil || f.Host == "" || f.Port == "" {
		return "", "", nil, false
	}
	return f.Host, f.Port, normalizeFingerprints(f.Fingerprints), true
}

// Save records the hub and its pin set: in memory at once, and in hub.json
// through a temp file beside it (0600) renamed over the old one, so a reader
// never sees a half-written file.
func (s *HubStore) Save(host, port string, fps []string) error {
	fps = normalizeFingerprints(fps)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.host, s.port, s.fps = host, port, fps
	raw, err := json.Marshal(hubFile{Host: host, Port: port, Fingerprints: fps})
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

// normalizeFingerprints lower-cases and trims each fingerprint and drops
// empty and repeated ones; the result is never nil (hub.json writes []).
func normalizeFingerprints(in []string) []string {
	out := make([]string, 0, len(in))
	for _, fp := range in {
		fp = strings.ToLower(strings.TrimSpace(fp))
		if fp != "" && !slices.Contains(out, fp) {
			out = append(out, fp)
		}
	}
	return out
}
