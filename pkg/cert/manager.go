package cert

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"
)

type Manager struct {
	caPrivateKey  crypto.Signer // allows RSA or ECDSA
	caCertificate *x509.Certificate
	caKeyPath     string
	caCertPath    string
}

func NewCertManager(caKeyPath, caCertPath string) (*Manager, error) {
	manager := &Manager{
		caKeyPath:  caKeyPath,
		caCertPath: caCertPath,
	}

	keyData, err := os.ReadFile(caKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA private key: %w", err)
	}

	keyBlock, _ := pem.Decode(keyData)
	if keyBlock == nil {
		return nil, errors.New("failed to decode CA private key PEM")
	}

	// Parse ECDSA P-256 private key only.
	privateKeyInterface, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		// Fallback to legacy EC format.
		if ecKey, err2 := x509.ParseECPrivateKey(keyBlock.Bytes); err2 == nil {
			privateKeyInterface = ecKey
		} else {
			return nil, fmt.Errorf("failed to parse CA private key: %w", err)
		}
	}

	ecKey, ok := privateKeyInterface.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("unsupported CA private key type %T – only ECDSA P-256 is allowed", privateKeyInterface)
	}

	if ecKey.Curve != elliptic.P256() {
		return nil, fmt.Errorf("CA private key must be generated with prime256v1 (P-256) curve")
	}

	manager.caPrivateKey = ecKey

	certData, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}

	certBlock, _ := pem.Decode(certData)
	if certBlock == nil {
		return nil, errors.New("failed to decode CA certificate PEM")
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}
	manager.caCertificate = cert

	return manager, nil
}

func (cm *Manager) SignCSR(csrData string) (string, string, time.Time, error) {
	// Decode PEM-encoded CSR
	csrBlock, _ := pem.Decode([]byte(csrData))
	if csrBlock == nil {
		return "", "", time.Time{}, errors.New("failed to decode CSR PEM")
	}

	// Parse CSR
	csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("failed to parse CSR: %w", err)
	}

	// Verify CSR signature
	if err := csr.CheckSignature(); err != nil {
		return "", "", time.Time{}, fmt.Errorf("invalid CSR signature: %w", err)
	}

	// Prepare certificate template
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("failed to generate serial number: %w", err)
	}

	expiresAt := time.Now().AddDate(15, 0, 0)
	// Create certificate template
	template := x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               csr.Subject,
		NotBefore:             time.Now(),
		NotAfter:              expiresAt,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	// Create certificate
	certDER, err := x509.CreateCertificate(
		rand.Reader,
		&template,
		cm.caCertificate,
		csr.PublicKey,
		cm.caPrivateKey,
	)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Encode certificate to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	return string(certPEM), csr.Subject.CommonName, expiresAt, nil
}
