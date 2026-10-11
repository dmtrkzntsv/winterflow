// Package storages keeps the agent's storage registrations under
// {AGENT_DATA_DIR}/storages/: one <name>.json per storage, mode 0600 in a
// 0700 folder, each written atomically (temp file, fsync, rename, folder
// fsync). An S3 secret stays ECIES ciphertext at rest; it is decrypted only
// in memory, or into an app's 0600 gitignored storage.env at deploy. The
// folder is the source of truth the API's server_storages index mirrors.
package storages

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"winterflow/internal/domain/model"
)

// Registry reads and writes the storages/ folder. Methods are safe for
// concurrent use.
type Registry struct {
	dir string
	mu  sync.Mutex
}

func NewRegistry(dir string) *Registry { return &Registry{dir: dir} }

// Dir is the folder the registry owns.
func (r *Registry) Dir() string { return r.dir }

func (r *Registry) path(name string) string { return filepath.Join(r.dir, name+".json") }

// List returns every storage, sorted by name. A file that does not parse,
// fails validation, or names another storage than its file name is skipped
// and logged: one damaged file must not hide the others.
func (r *Registry) List() ([]model.Storage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read storages: %w", err)
	}
	var out []model.Storage
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		s, err := r.read(name)
		if err != nil {
			slog.Warn("skipping unreadable storage registration", "file", e.Name(), "error", err)
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// read loads and checks one registration; callers hold r.mu.
func (r *Registry) read(name string) (model.Storage, error) {
	raw, err := os.ReadFile(r.path(name))
	if err != nil {
		return model.Storage{}, err
	}
	var s model.Storage
	if err := json.Unmarshal(raw, &s); err != nil {
		return model.Storage{}, err
	}
	if s.Name != name {
		return model.Storage{}, fmt.Errorf("names %q", s.Name)
	}
	if err := s.Validate(); err != nil {
		return model.Storage{}, err
	}
	return s, nil
}

// Get returns the storage named name; ok is false when there is none.
func (r *Registry) Get(name string) (model.Storage, bool, error) {
	if err := model.ValidateStorageName(name); err != nil {
		return model.Storage{}, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, err := r.read(name)
	if errors.Is(err, fs.ErrNotExist) {
		return model.Storage{}, false, nil
	}
	if err != nil {
		return model.Storage{}, false, fmt.Errorf("read storage %s: %w", name, err)
	}
	return s, true, nil
}

// Put writes (or replaces) a storage as given: callers resolve the
// "<encrypted>" placeholder first, so the stored secret is ciphertext.
func (r *Registry) Put(s model.Storage) error {
	s = s.Normalized()
	if err := s.Validate(); err != nil {
		return err
	}
	if s.S3 != nil && s.S3.SecretAccessKey == model.EncryptedSecret {
		return errors.New("refusing to store the <encrypted> placeholder as a secret")
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return fmt.Errorf("create storages dir: %w", err)
	}
	return writeAtomic(r.path(s.Name), raw)
}

// Delete removes a storage and reports whether it existed.
func (r *Registry) Delete(name string) (bool, error) {
	if err := model.ValidateStorageName(name); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	err := os.Remove(r.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("delete storage %s: %w", name, err)
	}
	syncDir(r.dir)
	return true, nil
}

// writeAtomic writes data (mode 0600) to path via a fsynced temp file that
// is renamed into place; the folder is fsynced after. A failed step removes
// the temp file, leaving the old content in place.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err == nil {
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	syncDir(filepath.Dir(path))
	return nil
}

// syncDir fsyncs a folder so a rename or removal in it is durable. Best
// effort: some filesystems refuse to sync a folder.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
