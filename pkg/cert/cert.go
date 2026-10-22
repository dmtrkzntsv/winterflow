package cert

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strings"
	"time"
)

// SignCSRAs issues a client-auth-only certificate for a CSR's public key with
// the subject set to exactly CN=commonName; the CSR's own subject is ignored.
// The CSR must carry a valid signature and an ECDSA P-256 key.
func SignCSRAs(csrPEM, commonName string, caCert *x509.Certificate, caKey crypto.Signer, validityYears int) (string, time.Time, error) {
	if caCert == nil || caKey == nil {
		return "", time.Time{}, fmt.Errorf("CA certificate and key are required")
	}
	if validityYears <= 0 {
		return "", time.Time{}, fmt.Errorf("validityYears must be positive")
	}
	if commonName == "" {
		return "", time.Time{}, fmt.Errorf("common name is required")
	}
	pub, err := parseP256CSR(csrPEM)
	if err != nil {
		return "", time.Time{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate serial number: %w", err)
	}
	now := time.Now()
	expiresAt := capToCA(now.AddDate(validityYears, 0, 0), caCert)
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              expiresAt,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, caCert, pub, caKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to create certificate: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), expiresAt, nil
}

// parseP256CSR decodes a PEM CSR, checks its signature and returns its
// ECDSA P-256 public key.
func parseP256CSR(csrPEM string) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil {
		return nil, fmt.Errorf("failed to decode CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("invalid CSR signature: %w", err)
	}
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, fmt.Errorf("CSR key must be ECDSA P-256")
	}
	return pub, nil
}

// SignHubCSR issues a server-auth-only certificate for a hub's CSR with the
// subject CN=hubID and a SAN for each of hosts (an IP address or a DNS name).
// The CSR's own subject and requested names are ignored.
func SignHubCSR(csrPEM, hubID string, hosts []string, caCert *x509.Certificate, caKey crypto.Signer, validityYears int) (string, time.Time, error) {
	if caCert == nil || caKey == nil {
		return "", time.Time{}, fmt.Errorf("CA certificate and key are required")
	}
	if validityYears <= 0 {
		return "", time.Time{}, fmt.Errorf("validityYears must be positive")
	}
	if hubID == "" || len(hosts) == 0 {
		return "", time.Time{}, fmt.Errorf("hub id and at least one host are required")
	}
	pub, err := parseP256CSR(csrPEM)
	if err != nil {
		return "", time.Time{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate serial number: %w", err)
	}
	now := time.Now()
	expiresAt := capToCA(now.AddDate(validityYears, 0, 0), caCert)
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: hubID},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              expiresAt,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		URIs:                  []*url.URL{HubURI(hubID)},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, caCert, pub, caKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to create certificate: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), expiresAt, nil
}

// Fingerprint returns the lowercase hex SHA-256 of the certificate's DER.
func Fingerprint(certPEM string) (string, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("failed to decode certificate PEM")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return "", fmt.Errorf("failed to parse certificate: %w", err)
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]), nil
}

// HubURIPrefix starts the URI SAN that marks a certificate as a hub's.
const HubURIPrefix = "winterflow-hub:"

// HubURI is the URI SAN a hub certificate carries: winterflow-hub:<id>, lower
// case (hub ids are).
func HubURI(hubID string) *url.URL {
	return &url.URL{Scheme: "winterflow-hub", Opaque: strings.ToLower(hubID)}
}

// HubIDFromCert returns the hub id in c's winterflow-hub URI SAN.
func HubIDFromCert(c *x509.Certificate) (string, bool) {
	for _, u := range c.URIs {
		if u.Scheme == "winterflow-hub" && u.Opaque != "" {
			return u.Opaque, true
		}
	}
	return "", false
}

// capToCA returns t, or the CA's NotAfter when that is earlier: no leaf
// outlives its issuer.
func capToCA(t time.Time, ca *x509.Certificate) time.Time {
	if ca != nil && t.After(ca.NotAfter) {
		return ca.NotAfter
	}
	return t
}
