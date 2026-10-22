package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// HubStore keeps the hub the API last named for this server in
// {AGENT_DATA_DIR}/hub.json; while it holds one, the agent dials it instead
// of HUB_HOST/HUB_PORT. Next to the address it keeps the fingerprints of the
// hub certificates the API trusts (the pin set; empty when the API named
// none or the file predates them), whether a non-empty set was ever stored
// (pinned: sticky, so an empty set read later means "trust no hub", not
// "check the CA only") and when the API last answered (refreshed_at, so a
// restart does not reset the refresh clock).
type HubStore struct {
	path string

	mu sync.Mutex
	// loaded is true once memory holds the file's state or the last save's:
	// a save still takes effect when the file cannot be written.
	loaded      bool
	host, port  string
	fps         []string
	pinned      bool
	refreshedAt time.Time
}

type hubFile struct {
	Host         string    `json:"host"`
	Port         string    `json:"port"`
	Fingerprints []string  `json:"fingerprints"`
	Pinned       bool      `json:"pinned,omitempty"`
	RefreshedAt  time.Time `json:"refreshed_at,omitzero"`
}

func NewHubStore(dataDir string) *HubStore {
	return &HubStore{path: filepath.Join(dataDir, "hub.json")}
}

// loadLocked reads hub.json into memory unless it is already there. A file
// that does not read as a host and a port leaves the store empty (and is
// read again next time). The caller holds mu.
func (s *HubStore) loadLocked() {
	if s.loaded {
		return
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var f hubFile
	if json.Unmarshal(raw, &f) != nil || f.Host == "" || f.Port == "" {
		return
	}
	s.loaded = true
	s.host, s.port = f.Host, f.Port
	s.fps = normalizeFingerprints(f.Fingerprints)
	// A file written before the flag existed is pinned when it holds a set.
	s.pinned = f.Pinned || len(s.fps) > 0
	s.refreshedAt = f.RefreshedAt
}

// Load returns the stored hub and its pin set (lower-case hex, possibly
// empty: a file without "fingerprints" reads as an empty set); ok is false
// when there is none (no file, or one that does not read as a host and a
// port), and fps is then empty too.
func (s *HubStore) Load() (host, port string, fps []string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	if !s.loaded {
		return "", "", nil, false
	}
	return s.host, s.port, slices.Clone(s.fps), true
}

// Pinned reports whether a non-empty pin set was ever stored. Once it is,
// an empty set means the API trusts no hub certificate.
func (s *HubStore) Pinned() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	return s.pinned
}

// RefreshedAt is when the API last answered an ask (zero when never).
func (s *HubStore) RefreshedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	return s.refreshedAt
}

// Save records the hub and its pin set, keeping the refresh time; storing a
// non-empty set pins the agent for good.
func (s *HubStore) Save(host, port string, fps []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	return s.saveLocked(host, port, fps, s.pinned, s.refreshedAt)
}

// SaveAnswer is Save for an answer the API gave at at: it also records the
// refresh time.
func (s *HubStore) SaveAnswer(host, port string, fps []string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	return s.saveLocked(host, port, fps, s.pinned, at)
}

// Reset replaces the whole state (enrollment: a new identity, possibly
// under another control plane): the agent is pinned only when fps is not
// empty, and has never refreshed.
func (s *HubStore) Reset(host, port string, fps []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(host, port, fps, false, time.Time{})
}

// saveLocked records the state: in memory at once, and in hub.json through
// a temp file beside it (0600) renamed over the old one, so a reader never
// sees a half-written file. The caller holds mu.
func (s *HubStore) saveLocked(host, port string, fps []string, pinned bool, at time.Time) error {
	fps = normalizeFingerprints(fps)
	pinned = pinned || len(fps) > 0
	s.loaded = true
	s.host, s.port, s.fps, s.pinned, s.refreshedAt = host, port, fps, pinned, at
	raw, err := json.Marshal(hubFile{Host: host, Port: port, Fingerprints: fps, Pinned: pinned, RefreshedAt: at.UTC()})
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

// fingerprintRe is a normalized SHA-256 certificate fingerprint.
var fingerprintRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// validFingerprints reports whether every entry of fps, normalized, is a
// SHA-256 fingerprint; bad is the first one that is not.
func validFingerprints(fps []string) (bad string, ok bool) {
	for _, fp := range fps {
		if !fingerprintRe.MatchString(strings.ToLower(strings.TrimSpace(fp))) {
			return fp, false
		}
	}
	return "", true
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
