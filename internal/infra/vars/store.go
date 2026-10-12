// Package vars is the agent's shared-variables state: the last vars.sync
// snapshot ({AGENT_DATA_DIR}/vars/snapshot.json, 0600, ciphertext only),
// the resolver that turns it into effective values, and the scanner that
// finds which names an app references.
package vars

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"winterflow/internal/domain/command"
)

// Store holds the snapshot in memory and on disk.
type Store struct {
	path   string
	mu     sync.Mutex
	cur    command.SyncVarsRequest
	loaded bool
}

// NewStore keeps the snapshot at dir/snapshot.json. Nothing is read until
// first use.
func NewStore(dir string) *Store {
	return &Store{path: filepath.Join(dir, "snapshot.json")}
}

// Current returns the stored snapshot (zero before the first sync). A file
// that cannot be read or parsed counts as no snapshot; the next vars.sync
// replaces it.
func (s *Store) Current() command.SyncVarsRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	return s.cur
}

func (s *Store) loadLocked() {
	if s.loaded {
		return
	}
	s.loaded = true
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var snap command.SyncVarsRequest
	if json.Unmarshal(raw, &snap) == nil {
		s.cur = snap
	}
}

// Apply stores snap unless it is older than the stored one, so a reconnect
// sync racing a change push cannot roll values back. It reports whether
// snap was stored.
func (s *Store) Apply(snap command.SyncVarsRequest) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	if snap.Revision < s.cur.Revision {
		return false, nil
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return false, err
	}
	if err := writeFileAtomic(s.path, raw, 0o600); err != nil {
		return false, err
	}
	s.cur = snap
	return true, nil
}

// writeFileAtomic writes data to a temp file beside path, fsyncs it and
// renames it over path, so a crash leaves either the old or the new file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // no-op after the rename
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
