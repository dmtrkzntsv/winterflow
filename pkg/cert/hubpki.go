package cert

import (
	"crypto"
	"crypto/rand"
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
	hubCertFile   = "hub.crt"
	hubKeyFile    = "hub.key"

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
	hubCertPath, hubKeyPath := filepath.Join(dir, hubCertFile), filepath.Join(dir, hubKeyFile)

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
	caCert, caKey, err := LoadCA(caCertPath, caKeyPath)
	if err != nil {
		return false, err
	}

	dns, ips := splitHosts(hubHosts)
	if fileExists(hubCertPath) && fileExists(hubKeyPath) {
		if cur, _, err := loadCertificate(hubCertPath); err == nil &&
			cur.CheckSignatureFrom(caCert) == nil && sansEqual(cur, dns, ips) && time.Now().Before(cur.NotAfter) {
			return changed, nil
		}
	}

	if err := GeneratePrivateKey(hubKeyPath); err != nil {
		return false, err
	}
	hubKey, err := loadECPrivateKey(hubKeyPath)
	if err != nil {
		return false, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return false, fmt.Errorf("generate serial number: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: hubHosts[0]},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(hubPKIValidityYears, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &hubKey.PublicKey, caKey)
	if err != nil {
		return false, fmt.Errorf("create hub certificate: %w", err)
	}
	if err := saveCertificate(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), hubCertPath); err != nil {
		return false, err
	}
	return true, nil
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
