package cert

import (
	"crypto"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	certpkg "winterflow/pkg/cert"
)

// CASigner signs agent certificates with the control plane's CA.
type CASigner struct {
	caCert *x509.Certificate
	caKey  crypto.Signer
	caPEM  string
}

// NewCASigner loads the CA certificate and key.
func NewCASigner(certPath, keyPath string) (*CASigner, error) {
	caCert, caKey, err := certpkg.LoadCA(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	pemBytes, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("read CA certificate: %w", err)
	}
	return &CASigner{caCert: caCert, caKey: caKey, caPEM: string(pemBytes)}, nil
}

// Sign issues a client certificate with CN = commonName, valid for the
// same period as every other agent certificate.
func (s *CASigner) Sign(csrPEM, commonName string) (string, time.Time, error) {
	return certpkg.SignCSRAs(csrPEM, commonName, s.caCert, s.caKey, validityYears)
}

// SignHub issues a server certificate for a hub: CN = hubID, SANs = hosts.
func (s *CASigner) SignHub(csrPEM, hubID string, hosts []string) (string, time.Time, error) {
	return certpkg.SignHubCSR(csrPEM, hubID, hosts, s.caCert, s.caKey, validityYears)
}

// CACertPEM is the CA certificate agents pin the hub against.
func (s *CASigner) CACertPEM() string { return s.caPEM }
