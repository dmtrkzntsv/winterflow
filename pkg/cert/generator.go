package cert

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	caKeyFilename           = "ca.key"
	caCertFilename          = "ca.crt"
	serverKeyFilename       = "server.key"
	serverCSRFilename       = "server.csr"
	serverCertFilename      = "server.crt"
	serverFullChainFilename = "server_fullchain.crt"

	certificateValidityYears = 100
	defaultDirectoryPerm     = 0o755
)

const (
	countryCanada              = "CA"
	organizationName           = "WinterFlow.io"
	caOrganizationalUnit       = "CA"
	serverOrganizationalUnit   = "SERVER"
	caCommonNameValue          = "WinterFlow.io CA"
	serverCommonNameValue      = "winterflow.io"
	supportEmailAddress        = "info@winterflow.io"
	defaultCSRCommonName       = serverCommonNameValue
	defaultSubjectPaddingHours = 1
)

var (
	errMissingOutputPath = errors.New("cert: output path is not configured")

	emailAddressOID = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}
)

type Generator struct {
	outputPath string
	extPath    string
}

func NewGenerator(outputPath, extPath string) (*Generator, error) {
	if outputPath == "" {
		return nil, errMissingOutputPath
	}

	if extPath == "" {
		return nil, fmt.Errorf("cert: extension config path is not configured")
	}

	return &Generator{
		outputPath: outputPath,
		extPath:    extPath,
	}, nil
}

func (g *Generator) GenerateCAKey() error {
	if err := g.ensureOutputDir(); err != nil {
		return err
	}

	caKeyPath := g.join(caKeyFilename)
	if err := GeneratePrivateKey(caKeyPath); err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}

	return nil
}

func (g *Generator) GenerateCACertificate() error {
	if err := g.ensureOutputDir(); err != nil {
		return err
	}

	caKeyPath := g.join(caKeyFilename)
	caCertPath := g.join(caCertFilename)

	privateKey, err := loadECPrivateKey(caKeyPath)
	if err != nil {
		return fmt.Errorf("load CA private key: %w", err)
	}

	caCertPEM, err := createCACertificatePEM(privateKey)
	if err != nil {
		return fmt.Errorf("create CA certificate: %w", err)
	}

	if err := saveCertificate(string(caCertPEM), caCertPath); err != nil {
		return fmt.Errorf("store CA certificate: %w", err)
	}

	return nil
}

func (g *Generator) GenerateServerKey() error {
	if err := g.ensureOutputDir(); err != nil {
		return err
	}

	serverKeyPath := g.join(serverKeyFilename)
	if err := GeneratePrivateKey(serverKeyPath); err != nil {
		return fmt.Errorf("generate server key: %w", err)
	}

	return nil
}

func (g *Generator) GenerateServerCertificate() error {
	if err := g.ensureOutputDir(); err != nil {
		return err
	}

	serverKeyPath := g.join(serverKeyFilename)
	serverCSRPath := g.join(serverCSRFilename)
	serverCertPath := g.join(serverCertFilename)
	caKeyPath := g.join(caKeyFilename)
	caCertPath := g.join(caCertFilename)
	fullChainPath := g.join(serverFullChainFilename)

	caKey, err := loadECPrivateKey(caKeyPath)
	if err != nil {
		return fmt.Errorf("load CA private key: %w", err)
	}

	caCert, caCertPEM, err := loadCertificate(caCertPath)
	if err != nil {
		return fmt.Errorf("load CA certificate: %w", err)
	}

	csrPEM, err := CreateCSR(defaultCSRCommonName, serverKeyPath, serverCSRPath)
	if err != nil {
		return fmt.Errorf("create server CSR: %w", err)
	}

	csr, err := parseCSR(csrPEM)
	if err != nil {
		return fmt.Errorf("parse server CSR: %w", err)
	}

	dnsNames, ipAddresses, err := loadSubjectAltNames(g.extPath)
	if err != nil {
		return fmt.Errorf("load subject alternative names: %w", err)
	}

	serverCertPEM, err := createServerCertificatePEM(csr, caCert, caKey, dnsNames, ipAddresses)
	if err != nil {
		return fmt.Errorf("create server certificate: %w", err)
	}

	if err := saveCertificate(string(serverCertPEM), serverCertPath); err != nil {
		return fmt.Errorf("store server certificate: %w", err)
	}

	if err := writeFullChain(fullChainPath, serverCertPEM, caCertPEM); err != nil {
		return err
	}

	return nil
}

func (g *Generator) ensureOutputDir() error {
	if err := os.MkdirAll(g.outputPath, defaultDirectoryPerm); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	return nil
}

func (g *Generator) join(filename string) string {
	return filepath.Join(g.outputPath, filename)
}

func createCACertificatePEM(privateKey *ecdsa.PrivateKey) ([]byte, error) {
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate CA serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               caSubject(),
		NotBefore:             time.Now().Add(-defaultSubjectPaddingHours * time.Hour),
		NotAfter:              time.Now().AddDate(certificateValidityYears, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	if subjectKeyID, err := subjectKeyID(&privateKey.PublicKey); err == nil {
		template.SubjectKeyId = subjectKeyID
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, fmt.Errorf("create CA certificate: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	}), nil
}

func createServerCertificatePEM(csr *x509.CertificateRequest, caCert *x509.Certificate, caKey crypto.Signer, dns []string, ips []net.IP) ([]byte, error) {
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate server serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               serverSubject(),
		NotBefore:             time.Now().Add(-defaultSubjectPaddingHours * time.Hour),
		NotAfter:              time.Now().AddDate(certificateValidityYears, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           ips,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, csr.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("create server certificate: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	}), nil
}

func caSubject() pkix.Name {
	return pkix.Name{
		Country:            []string{countryCanada},
		Organization:       []string{organizationName},
		OrganizationalUnit: []string{caOrganizationalUnit},
		CommonName:         caCommonNameValue,
		ExtraNames: []pkix.AttributeTypeAndValue{
			{
				Type:  emailAddressOID,
				Value: supportEmailAddress,
			},
		},
	}
}

func serverSubject() pkix.Name {
	return pkix.Name{
		Country:            []string{countryCanada},
		Organization:       []string{organizationName},
		OrganizationalUnit: []string{serverOrganizationalUnit},
		CommonName:         serverCommonNameValue,
		ExtraNames: []pkix.AttributeTypeAndValue{
			{
				Type:  emailAddressOID,
				Value: supportEmailAddress,
			},
		},
	}
}

func subjectKeyID(pub crypto.PublicKey) ([]byte, error) {
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("marshal public key: %w", err)
	}

	sum := sha1.Sum(pubKeyBytes)
	return sum[:], nil
}
