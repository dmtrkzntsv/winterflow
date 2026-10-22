package cert

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// File names match the config getters (GetHubCACertFilename etc.).
const (
	hubCACertFile = "ca.crt"
	hubCAKeyFile  = "ca.key"

	hubPKIValidityYears = 10
)

// LoadCA reads a CA certificate and its EC private key from disk.
func LoadCA(certPath, keyPath string) (*x509.Certificate, crypto.Signer, error) {
	caCert, _, err := loadCertificate(certPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load CA certificate: %w", err)
	}
	key, err := loadECPrivateKey(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load CA private key: %w", err)
	}
	return caCert, key, nil
}

// EnsureHubPKI makes dir hold a CA (never replaced once present) and a hub
// server certificate signed by it. The hub certificate is (re)issued, with a
// fresh key, when absent or when its SANs differ from hubHosts. It reports
// whether anything was written.
func EnsureHubPKI(dir string, hubHosts []string) (bool, error) {
	if len(hubHosts) == 0 {
		return false, fmt.Errorf("at least one hub host is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("create PKI directory: %w", err)
	}
	caCertPath, caKeyPath := filepath.Join(dir, hubCACertFile), filepath.Join(dir, hubCAKeyFile)

	changed := false
	caCertExists, caKeyExists := fileExists(caCertPath), fileExists(caKeyPath)
	switch {
	case caCertExists && caKeyExists:
	case caCertExists != caKeyExists:
		return false, fmt.Errorf("incomplete CA in %s: need both %s and %s", dir, hubCACertFile, hubCAKeyFile)
	default:
		if err := createCA(caCertPath, caKeyPath); err != nil {
			return false, err
		}
		changed = true
	}
	serverChanged, err := EnsureServerCert(dir, "hub", hubHosts)
	if err != nil {
		return false, err
	}
	return changed || serverChanged, nil
}

// EnsureServerCert makes dir hold a server certificate <base>.crt / <base>.key
// for hosts, signed by the CA already in dir (ca.crt / ca.key; see
// EnsureHubPKI). It is (re)issued, with a fresh key, when absent, not signed by
// the CA, expired, or when its SANs differ from hosts. It reports whether
// anything was written.
func EnsureServerCert(dir, base string, hosts []string) (bool, error) {
	if len(hosts) == 0 {
		return false, fmt.Errorf("at least one host is required")
	}
	if base == "" || base != filepath.Base(base) {
		return false, fmt.Errorf("invalid certificate name %q", base)
	}
	caCert, caKey, err := LoadCA(filepath.Join(dir, hubCACertFile), filepath.Join(dir, hubCAKeyFile))
	if err != nil {
		return false, err
	}
	certPath, keyPath := filepath.Join(dir, base+".crt"), filepath.Join(dir, base+".key")

	dns, ips := splitHosts(hosts)
	if fileExists(certPath) && fileExists(keyPath) {
		if cur, _, err := loadCertificate(certPath); err == nil &&
			cur.CheckSignatureFrom(caCert) == nil && sansEqual(cur, dns, ips) && time.Now().Before(cur.NotAfter) {
			if _, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
				return false, nil
			}
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return false, fmt.Errorf("generate %s key: %w", base, err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return false, fmt.Errorf("marshal %s key: %w", base, err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return false, fmt.Errorf("generate serial number: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: hosts[0]},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(hubPKIValidityYears, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return false, fmt.Errorf("create %s certificate: %w", base, err)
	}
	// Key first, certificate last, each by a rename: a crash in between leaves
	// a pair that does not match, which the check above reissues.
	if err := writeFileAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return false, err
	}
	if err := writeFileAtomic(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// writeFileAtomic writes data to a temp file beside path, syncs it and
// renames it over path.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", path, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func createCA(certPath, keyPath string) error {
	if err := GeneratePrivateKey(keyPath); err != nil {
		return err
	}
	key, err := loadECPrivateKey(keyPath)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generate serial number: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Winterflow CA", Organization: []string{"Winterflow"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(hubPKIValidityYears, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	if id, err := subjectKeyID(&key.PublicKey); err == nil {
		tmpl.SubjectKeyId = id
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create CA certificate: %w", err)
	}
	return saveCertificate(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), certPath)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func splitHosts(hosts []string) ([]string, []net.IP) {
	var dns []string
	var ips []net.IP
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			ips = append(ips, ip)
		} else {
			dns = append(dns, h)
		}
	}
	return dns, ips
}

func sansEqual(c *x509.Certificate, dns []string, ips []net.IP) bool {
	a := append([]string(nil), c.DNSNames...)
	b := append([]string(nil), dns...)
	for _, ip := range c.IPAddresses {
		a = append(a, "ip:"+ip.String())
	}
	for _, ip := range ips {
		b = append(b, "ip:"+ip.String())
	}
	sort.Strings(a)
	sort.Strings(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
