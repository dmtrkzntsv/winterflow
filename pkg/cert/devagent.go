package cert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IssueAgentCert writes a CA-signed agent identity into outDir for local
// development and tests: a fresh P-256 agent.key (0600), agent.crt with
// CN = serverID (client auth, signed by the CA in caDir as enrollment would)
// and, when outDir is another directory, a copy of ca.crt. Production agents
// get theirs from enrollment instead.
func IssueAgentCert(caDir, outDir, serverID string) error {
	serverID = strings.TrimSpace(serverID)
	if caDir == "" || outDir == "" || serverID == "" {
		return fmt.Errorf("agent certificate: CA directory, output directory and server id are required")
	}
	caCertPath := filepath.Join(caDir, hubCACertFile)
	caCert, caKey, err := LoadCA(caCertPath, filepath.Join(caDir, hubCAKeyFile))
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate agent key: %w", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: serverID}}, key)
	if err != nil {
		return fmt.Errorf("create agent CSR: %w", err)
	}
	certPEM, _, err := SignCSRAs(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})), serverID, caCert, caKey, hubPKIValidityYears)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal agent key: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", outDir, err)
	}
	if err := writeFileAtomic(filepath.Join(outDir, AgentKeyFile), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(outDir, AgentCertFile), []byte(certPEM), 0o600); err != nil {
		return err
	}
	if filepath.Clean(outDir) == filepath.Clean(caDir) {
		return nil
	}
	caPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return fmt.Errorf("read CA certificate: %w", err)
	}
	return writeFileAtomic(filepath.Join(outDir, hubCACertFile), caPEM, 0o644)
}
