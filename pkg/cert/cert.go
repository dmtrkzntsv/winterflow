package cert

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// GeneratePrivateKey generates a new ECDSA P-256 private key and saves it to the specified path
func GeneratePrivateKey(keyPath string) error {
	// Create directory if it doesn't exist
	dir := filepath.Dir(keyPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory for private key: %v", err)
	}

	// Generate ECDSA P-256 private key
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate ECDSA private key: %v", err)
	}

	keyBytes, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("failed to marshal ECDSA private key: %v", err)
	}

	// Convert to PEM format
	privateKeyPEM := &pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyBytes,
	}

	// Create file
	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create private key file: %v", err)
	}
	defer keyFile.Close()

	// Write PEM to file
	if err := pem.Encode(keyFile, privateKeyPEM); err != nil {
		return fmt.Errorf("failed to write private key to file: %v", err)
	}

	return nil
}

// CreateCSR creates a Certificate Signing Request with the given private key and saves it to the specified path
func CreateCSR(certificateID string, privateKeyPath, csrPath string) (string, error) {
	// Create directory if it doesn't exist
	dir := filepath.Dir(csrPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory for CSR: %v", err)
	}

	// Read private key
	keyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return "", fmt.Errorf("failed to read private key: %v", err)
	}

	// Parse private key (prime256v1 ECDSA only)
	block, _ := pem.Decode(keyData)
	if block == nil {
		return "", fmt.Errorf("failed to parse private key PEM")
	}

	if block.Type != "EC PRIVATE KEY" {
		return "", fmt.Errorf("unsupported private key type: %s (only EC P-256 is allowed)", block.Type)
	}

	parsedKey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse EC private key: %v", err)
	}

	if parsedKey.Curve != elliptic.P256() {
		return "", fmt.Errorf("unsupported EC curve %s: expected P-256", parsedKey.Curve.Params().Name)
	}

	// Create CSR template
	template := x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: certificateID,
		},
	}

	// Create CSR
	csrBytes, err := x509.CreateCertificateRequest(rand.Reader, &template, parsedKey)
	if err != nil {
		return "", fmt.Errorf("failed to create CSR: %v", err)
	}

	// Convert to PEM format
	csrPEM := &pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrBytes,
	}

	// Create file
	csrFile, err := os.OpenFile(csrPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return "", fmt.Errorf("failed to create CSR file: %v", err)
	}
	defer csrFile.Close()

	// Write PEM to file
	if err := pem.Encode(csrFile, csrPEM); err != nil {
		return "", fmt.Errorf("failed to write CSR to file: %v", err)
	}

	// Convert CSR to string for API submission
	var csrBuffer bytes.Buffer
	if err := pem.Encode(&csrBuffer, csrPEM); err != nil {
		return "", fmt.Errorf("failed to encode CSR to string: %v", err)
	}

	log.Printf("[DEBUG] Created CSR at: %s with Common Name: %s", csrPath, certificateID)
	return csrBuffer.String(), nil
}

// SignCSR issues a certificate from a PEM-encoded CSR using the provided CA materials.
// validityYears defines how many years from now the certificate should remain valid.
func SignCSR(csrData string, caCert *x509.Certificate, caKey crypto.Signer, validityYears int) (string, string, time.Time, error) {
	if caCert == nil {
		return "", "", time.Time{}, fmt.Errorf("CA certificate is required")
	}
	if caKey == nil {
		return "", "", time.Time{}, fmt.Errorf("CA private key is required")
	}
	if validityYears <= 0 {
		return "", "", time.Time{}, fmt.Errorf("validityYears must be positive")
	}

	csrBlock, _ := pem.Decode([]byte(csrData))
	if csrBlock == nil {
		return "", "", time.Time{}, fmt.Errorf("failed to decode CSR PEM")
	}

	csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("failed to parse CSR: %w", err)
	}

	if err := csr.CheckSignature(); err != nil {
		return "", "", time.Time{}, fmt.Errorf("invalid CSR signature: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("failed to generate serial number: %w", err)
	}

	expiresAt := time.Now().AddDate(validityYears, 0, 0)
	template := x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               csr.Subject,
		NotBefore:             time.Now(),
		NotAfter:              expiresAt,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, caCert, csr.PublicKey, caKey)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("failed to create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	return string(certPEM), csr.Subject.CommonName, expiresAt, nil
}
