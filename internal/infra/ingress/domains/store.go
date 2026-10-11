// Package domains stores the agent's domain registrations under
// {AGENT_DATA_DIR}/domains/: one <name>.json per registration, plus in
// certificate mode the pair <name>.<fp>.crt (0644) and <name>.<fp>.key
// (0600), where fp is the first 12 hex digits of sha256(cert || key). A
// wildcard's "*" is spelled _wildcard_ in file names. The directory is the
// source of truth the API's server_domains index mirrors.
//
// Certificate files are content-addressed: a renewal writes the new pair
// next to the old one, then switches the .json (written last, atomically) to
// it, then prunes the old pair. The .json therefore only ever names a
// complete pair, and the generated Caddy config changes whenever the
// certificate does. Every file is fsynced before its rename and the directory
// after it, so a power cut cannot leave the .json naming a pair whose bytes
// never reached the disk.
package domains

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"winterflow/internal/domain/model"
)

// Registration is one stored registration. In certificate mode the .json
// also records the certificate's expiry, issuer and Fingerprint (which names
// the pair files); CertFile and KeyFile are derived from it.
type Registration struct {
	model.RegisteredDomain
	Fingerprint string `json:"fingerprint,omitempty"`
	CertFile    string `json:"-"`
	KeyFile     string `json:"-"`
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

const (
	wildcardToken = "_wildcard_"
	// fingerprintLen hex digits of sha256(cert || key) name a pair.
	fingerprintLen = 12
)

// fileStem maps a validated name to its file stem ("*.x.app" ->
// "_wildcard_.x.app"); nameOf is the inverse.
func fileStem(name string) string { return strings.Replace(name, "*", wildcardToken, 1) }
func nameOf(stem string) string   { return strings.Replace(stem, wildcardToken, "*", 1) }

func (s *Store) jsonPath(name string) string {
	return filepath.Join(s.dir, fileStem(name)+".json")
}

// pairPaths are the certificate and key files of name's pair fp.
func (s *Store) pairPaths(name, fp string) (string, string) {
	base := filepath.Join(s.dir, fileStem(name)+"."+fp)
	return base + ".crt", base + ".key"
}

func fingerprint(certPEM, keyPEM []byte) string {
	h := sha256.New()
	h.Write(certPEM)
	h.Write(keyPEM)
	return hex.EncodeToString(h.Sum(nil))[:fingerprintLen]
}

func isFingerprint(fp string) bool {
	if len(fp) != fingerprintLen {
		return false
	}
	_, err := hex.DecodeString(fp)
	return err == nil
}

// Exists reports whether the domains/ directory exists (the upgrade path
// seeds it once, on the first start without it).
func (s *Store) Exists() bool {
	_, err := os.Stat(s.dir)
	return err == nil
}

// List returns every registration, sorted by name. A file that does not
// parse, names something else than its file name, carries an unknown mode,
// or (certificate mode) points at a pair that is missing or does not load is
// skipped: its hosts then fall out of ingress (fail closed) instead of one
// broken file stopping all routing — Caddy refuses its whole TLS app when one
// loaded pair is damaged.
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
		if r.TLS == model.DomainTLSCertificate {
			if !isFingerprint(r.Fingerprint) {
				slog.Warn("skipping domain registration without a certificate fingerprint", "domain", r.Name)
				continue
			}
			r.CertFile, r.KeyFile = s.pairPaths(r.Name, r.Fingerprint)
			if err := loadPair(r.CertFile, r.KeyFile); err != nil {
				slog.Warn("skipping domain registration whose certificate files are missing or damaged", "domain", r.Name, "error", err)
				continue
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// loadPair checks that a pair on disk loads the way Caddy's load_files will
// load it: both files present, parseable, and the key matching. A single
// parse of two small files — cheap next to the outage a damaged pair causes.
func loadPair(crt, key string) error {
	_, err := tls.LoadX509KeyPair(crt, key)
	return err
}

// Put writes (or replaces) a registration and returns it as stored. In
// certificate mode the pair is checked first (CheckCertificate) and written
// under its fingerprint; nothing is written when the check fails, and the
// previous pair keeps serving until the .json switches to the new one.
// Switching away from certificate mode removes the pair.
func (s *Store) Put(name string, mode model.DomainTLS, certPEM, keyPEM []byte) (Registration, error) {
	if err := model.ValidateDomainName(name); err != nil {
		return Registration{}, err
	}
	if !mode.Valid() {
		return Registration{}, fmt.Errorf("unknown tls mode %q", mode)
	}
	reg := Registration{RegisteredDomain: model.RegisteredDomain{Name: name, TLS: mode}}
	if mode == model.DomainTLSCertificate {
		leaf, err := CheckCertificate(name, certPEM, keyPEM, time.Now())
		if err != nil {
			return Registration{}, err
		}
		notAfter := leaf.NotAfter.UTC()
		reg.NotAfter, reg.Issuer = &notAfter, leaf.Issuer.CommonName
		reg.Fingerprint = fingerprint(certPEM, keyPEM)
		reg.CertFile, reg.KeyFile = s.pairPaths(name, reg.Fingerprint)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return Registration{}, fmt.Errorf("create domains dir: %w", err)
	}
	if mode == model.DomainTLSCertificate {
		if err := writePair(reg.CertFile, reg.KeyFile, certPEM, keyPEM); err != nil {
			return Registration{}, err
		}
	}
	raw, _ := json.Marshal(reg)
	if err := writeAtomic(s.jsonPath(name), raw, 0o644); err != nil {
		return Registration{}, err
	}
	s.prunePairs(name, reg.Fingerprint)
	return reg, nil
}

// writePair installs a content-addressed pair. A pair already on disk under
// these names that loads has this very content, so it is left alone (a
// damaged one is rewritten). On a failure the pair's files are removed
// again: an incomplete pair is never the one a .json names.
func writePair(crt, key string, certPEM, keyPEM []byte) error {
	if loadPair(crt, key) == nil {
		return nil
	}
	err := writeAtomic(crt, certPEM, 0o644)
	if err == nil {
		err = writeAtomic(key, keyPEM, 0o600)
	}
	if err != nil {
		_ = os.Remove(crt)
		_ = os.Remove(key)
	}
	return err
}

// prunePairs removes name's pair files except the pair keep ("" removes
// all), and any temp file name's writes left behind (a crash between write
// and rename). A file belongs to name when it is exactly
// <stem>.<fingerprint>.crt or .key, or <stem>.json, with an optional .tmp —
// parsing, not globbing, so "x.app" never prunes "x.app.y"'s files. Callers
// hold s.mu, so no write of name's is in progress.
func (s *Store) prunePairs(name, keep string) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	stem := fileStem(name)
	for _, e := range entries {
		file, isTmp := strings.CutSuffix(e.Name(), ".tmp")
		if isTmp && file == stem+".json" {
			_ = os.Remove(filepath.Join(s.dir, e.Name()))
			continue
		}
		base, ok := strings.CutSuffix(file, ".crt")
		if !ok {
			base, ok = strings.CutSuffix(file, ".key")
		}
		i := strings.LastIndex(base, ".")
		if !ok || i < 0 || base[:i] != stem || !isFingerprint(base[i+1:]) || (!isTmp && base[i+1:] == keep) {
			continue
		}
		_ = os.Remove(filepath.Join(s.dir, e.Name()))
	}
}

// Delete removes a registration and its certificate files. It reports
// whether the registration existed; orphan pair and temp files for name are
// removed either way.
func (s *Store) Delete(name string) (bool, error) {
	if err := model.ValidateDomainName(name); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.jsonPath(name))
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("delete domain: %w", err)
	}
	s.prunePairs(name, "")
	if err != nil {
		return false, nil
	}
	syncDir(s.dir)
	return true, nil
}

// Seed creates the domains/ directory holding regs (auto or off), unless it
// already exists; it reports whether it did. The registrations are Put into
// a sibling staging directory that is renamed into place, so a crash mid-way
// leaves no half-seeded directory that would stop the next start from
// seeding.
func (s *Store) Seed(regs []model.RegisteredDomain) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.dir); err == nil {
		return false, nil
	}
	tmp := s.dir + ".seed"
	_ = os.RemoveAll(tmp)
	staging := NewStore(tmp)
	err := os.MkdirAll(tmp, 0o755)
	for _, r := range regs {
		if err != nil {
			break
		}
		_, err = staging.Put(r.Name, r.TLS, nil, nil)
	}
	if err == nil {
		err = os.Rename(tmp, s.dir)
	}
	if err != nil {
		_ = os.RemoveAll(tmp)
		return false, fmt.Errorf("seed domains: %w", err)
	}
	syncDir(filepath.Dir(s.dir))
	return true, nil
}

// writeAtomic writes data to path via a temp file that is fsynced, renamed
// into place, and made durable by fsyncing the directory, removing the temp
// file when a step fails. After a power cut path holds the old content or
// the new, never a truncated file.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	err := writeSynced(tmp, data, perm)
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

// writeSynced is os.WriteFile plus an fsync before the close.
func writeSynced(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// syncDir fsyncs a directory so the renames and removals in it are durable.
// Best effort: some filesystems refuse to sync a directory, and the files
// themselves are already synced.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
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

// leafCovers reports whether a SAN of leaf covers name under the
// registration rule. A wildcard name's "*" counts as the one label, so only
// the same wildcard SAN covers it; an exact name is covered by itself or by
// the wildcard SAN of its parent.
func leafCovers(leaf *x509.Certificate, name string) bool {
	for _, san := range leaf.DNSNames {
		if model.DomainCovers(san, name) {
			return true
		}
	}
	return false
}
