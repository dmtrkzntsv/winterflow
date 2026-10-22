package cert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// File names of the agent's identity (match the config getters
// GetAgentKeyFilename / GetAgentCertFilename).
const (
	AgentKeyFile  = "agent.key"
	AgentCertFile = "agent.crt"

	agentIdentityValidityYears = 10
)

// EnsureAgentIdentity makes dir hold an agent identity for serverID: a P-256
// agent.key (0600) and a certificate with CN = serverID. A matching pair
// already there (key and certificate belong together, CN = serverID) is kept
// as is, whoever signed it: the agent's secrets are encrypted to that key.
// Otherwise both are created, the certificate self-signed (client auth, 10
// years), each file by a rename of a temp file beside it; a crash between the
// two leaves a mismatched pair the next call replaces. It returns the
// certificate PEM and whether it created the pair.
func EnsureAgentIdentity(dir, serverID string) ([]byte, bool, error) {
	if dir == "" {
		return nil, false, fmt.Errorf("agent identity: directory is not configured")
	}
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		return nil, false, fmt.Errorf("agent identity: server id is required")
	}
	if cn, certPEM, ok := loadAgentIdentity(dir); ok && cn == serverID {
		return certPEM, false, nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, false, fmt.Errorf("generate agent key: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, false, fmt.Errorf("marshal agent key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, false, fmt.Errorf("generate serial number: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: serverID},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(agentIdentityValidityYears, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, false, fmt.Errorf("create agent certificate: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, fmt.Errorf("create agent identity directory: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, AgentKeyFile), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, false, err
	}
	if err := writeFileAtomic(filepath.Join(dir, AgentCertFile), certPEM, 0o600); err != nil {
		return nil, false, err
	}
	return certPEM, true, nil
}

// AgentIdentityID returns the CN of the agent identity in dir when its key
// and certificate belong together.
func AgentIdentityID(dir string) (string, bool) {
	cn, _, ok := loadAgentIdentity(dir)
	return cn, ok
}

// loadAgentIdentity reads dir's agent.key and agent.crt and reports the
// certificate's CN and PEM when the key is a P-256 key whose public half is
// the certificate's and the CN is set.
func loadAgentIdentity(dir string) (string, []byte, bool) {
	if dir == "" {
		return "", nil, false
	}
	key, err := loadECPrivateKey(filepath.Join(dir, AgentKeyFile))
	if err != nil {
		return "", nil, false
	}
	c, certPEM, err := loadCertificate(filepath.Join(dir, AgentCertFile))
	if err != nil {
		return "", nil, false
	}
	pub, ok := c.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) || c.Subject.CommonName == "" {
		return "", nil, false
	}
	return c.Subject.CommonName, certPEM, true
}
