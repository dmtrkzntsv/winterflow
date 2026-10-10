// Package domains stores the agent's domain registrations under
// {AGENT_DATA_DIR}/domains/: one <name>.json per registration ({name, tls}),
// plus <name>.crt (0644) and <name>.key (0600) in certificate mode. A
// wildcard's "*" is spelled _wildcard_ in file names. The directory is the
// source of truth the API's server_domains index mirrors.
package domains

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// Registration is one stored registration. CertFile and KeyFile are set in
// certificate mode only.
type Registration struct {
	Name     string            `json:"name"`
	TLS      command.DomainTLS `json:"tls"`
	CertFile string            `json:"-"`
	KeyFile  string            `json:"-"`
}

// Store reads and writes the domains/ directory. Methods are safe for
// concurrent use.
type Store struct {
	dir string
	mu  sync.Mutex
}

func NewStore(dir string) *Store { return &Store{dir: dir} }

// Dir is the directory the store owns.
func (s *Store) Dir() string { return s.dir }

const wildcardToken = "_wildcard_"

// fileStem maps a validated name to its file stem ("*.x.app" ->
// "_wildcard_.x.app"); nameOf is the inverse.
func fileStem(name string) string { return strings.Replace(name, "*", wildcardToken, 1) }
func nameOf(stem string) string   { return strings.Replace(stem, wildcardToken, "*", 1) }

func (s *Store) path(name, ext string) string {
	return filepath.Join(s.dir, fileStem(name)+ext)
}

// Exists reports whether the domains/ directory exists (the upgrade path
// seeds it once, on the first start without it).
func (s *Store) Exists() bool {
	_, err := os.Stat(s.dir)
	return err == nil
}

// List returns every registration, sorted by name. A file that does not
// parse, names something else than its file name, or carries an unknown
// mode is skipped: its hosts then fall out of ingress (fail closed) instead
// of one broken file stopping all routing.
func (s *Store) List() ([]Registration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read domains: %w", err)
	}
	var out []Registration
	for _, e := range entries {
		stem, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var r Registration
		if json.Unmarshal(raw, &r) != nil || r.Name != nameOf(stem) ||
			model.ValidateDomainName(r.Name) != nil || !r.TLS.Valid() {
			continue
		}
		if r.TLS == command.DomainTLSCertificate {
			r.CertFile, r.KeyFile = s.path(r.Name, ".crt"), s.path(r.Name, ".key")
			// A renewal replaces two files; a crash between the renames
			// leaves a mismatched pair. Fail closed rather than serve it.
			if _, err := tls.LoadX509KeyPair(r.CertFile, r.KeyFile); err != nil {
				slog.Warn("skipping domain registration with an unusable certificate pair", "domain", r.Name, "error", err)
				continue
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Put writes (or replaces) a registration. In certificate mode certPEM and
// keyPEM are written next to it; switching away from certificate mode removes
// them. The .json is written last so a crash never leaves a registration
// pointing at missing files.
func (s *Store) Put(name string, mode command.DomainTLS, certPEM, keyPEM []byte) error {
	if err := model.ValidateDomainName(name); err != nil {
		return err
	}
	if !mode.Valid() {
		return fmt.Errorf("unknown tls mode %q", mode)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create domains dir: %w", err)
	}
	if mode == command.DomainTLSCertificate {
		// Stage both files first, then rename back-to-back, so a failure
		// while writing never touches the working pair.
		crt, key := s.path(name, ".crt"), s.path(name, ".key")
		if err := stage(crt+".tmp", certPEM, 0o644); err != nil {
			return err
		}
		if err := stage(key+".tmp", keyPEM, 0o600); err != nil {
			_ = os.Remove(crt + ".tmp")
			return err
		}
		if err := os.Rename(crt+".tmp", crt); err != nil {
			_ = os.Remove(crt + ".tmp")
			_ = os.Remove(key + ".tmp")
			return fmt.Errorf("write %s: %w", filepath.Base(crt), err)
		}
		if err := os.Rename(key+".tmp", key); err != nil {
			_ = os.Remove(key + ".tmp")
			return fmt.Errorf("write %s: %w", filepath.Base(key), err)
		}
	}
	raw, _ := json.Marshal(Registration{Name: name, TLS: mode})
	if err := writeAtomic(s.path(name, ".json"), raw, 0o644); err != nil {
		return err
	}
	if mode != command.DomainTLSCertificate {
		_ = os.Remove(s.path(name, ".crt"))
		_ = os.Remove(s.path(name, ".key"))
	}
	return nil
}

// Delete removes a registration and its certificate files. It reports
// whether the registration existed.
func (s *Store) Delete(name string) (bool, error) {
	if err := model.ValidateDomainName(name); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path(name, ".json"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("delete domain: %w", err)
	}
	_ = os.Remove(s.path(name, ".crt"))
	_ = os.Remove(s.path(name, ".key"))
	return true, nil
}

// Seed creates the domains/ directory holding regs, unless it already
// exists; it reports whether it did. The files are written to a sibling
// directory renamed into place, so a crash mid-way leaves no half-seeded
// directory that would stop the next start from seeding.
func (s *Store) Seed(regs []Registration) (bool, error) {
	for _, r := range regs {
		if err := model.ValidateDomainName(r.Name); err != nil {
			return false, err
		}
		if !r.TLS.Valid() {
			return false, fmt.Errorf("unknown tls mode %q for %s", r.TLS, r.Name)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.dir); err == nil {
		return false, nil
	}
	tmp := s.dir + ".seed"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return false, fmt.Errorf("seed domains: %w", err)
	}
	for _, r := range regs {
		raw, _ := json.Marshal(Registration{Name: r.Name, TLS: r.TLS})
		if err := os.WriteFile(filepath.Join(tmp, fileStem(r.Name)+".json"), raw, 0o644); err != nil {
			return false, fmt.Errorf("seed domains: %w", err)
		}
	}
	if err := os.Rename(tmp, s.dir); err != nil {
		return false, fmt.Errorf("seed domains: %w", err)
	}
	return true, nil
}

// stage writes data to tmp, removing the partial file if the write fails.
func stage(tmp string, data []byte, perm os.FileMode) error {
	if err := os.WriteFile(tmp, data, perm); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", filepath.Base(tmp), err)
	}
	return nil
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := stage(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// CheckCertificate validates an uploaded pair for name at now: the key must
// match the certificate, the leaf must cover name (the same wildcard SAN for
// a wildcard name; the exact name or a wildcard SAN covering it for an exact
// name), and the leaf must not be expired. It returns the parsed leaf.
func CheckCertificate(name string, certPEM, keyPEM []byte, now time.Time) (*x509.Certificate, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("the certificate and key do not match or are not PEM: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	if !leafCovers(leaf, name) {
		sans := strings.Join(leaf.DNSNames, ", ")
		if sans == "" {
			sans = "no DNS names"
		}
		return nil, fmt.Errorf("the certificate does not cover %s (it covers %s)", name, sans)
	}
	if now.After(leaf.NotAfter) {
		return nil, fmt.Errorf("the certificate expired on %s", leaf.NotAfter.UTC().Format(time.DateOnly))
	}
	return leaf, nil
}

func leafCovers(leaf *x509.Certificate, name string) bool {
	for _, san := range leaf.DNSNames {
		san = strings.ToLower(san)
		if san == name {
			return true
		}
		if !model.IsWildcardDomain(name) && model.IsWildcardDomain(san) && model.DomainCovers(san, name) {
			return true
		}
	}
	return false
}

// CertInfo reads the leaf of a stored PEM certificate: its expiry and its
// issuer's common name.
func CertInfo(certFile string) (time.Time, string, error) {
	raw, err := os.ReadFile(certFile)
	if err != nil {
		return time.Time{}, "", err
	}
	var block *pem.Block
	for {
		block, raw = pem.Decode(raw)
		if block == nil {
			return time.Time{}, "", errors.New("no PEM certificate")
		}
		if block.Type == "CERTIFICATE" {
			break
		}
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, "", err
	}
	return leaf.NotAfter, leaf.Issuer.CommonName, nil
}
