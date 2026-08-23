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
	"strings"
	"time"
)

const defaultDirectoryPerm = 0o755
const subjectPaddingHours = 1

// Paths groups the filenames used for certificate generation relative to the output directory.
type Paths struct {
	CAKey           string
	CACert          string
	ServerKey       string
	ServerCSR       string
	ServerCert      string
	ServerFullChain string
	AgentKey        string
	AgentCSR        string
	AgentCert       string
}

var (
	errMissingOutputPath = errors.New("cert: output path is not configured")

	emailAddressOID = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}
)

type Generator struct {
	outputPath           string
	extPath              string
	paths                Paths
	caSubject            pkix.Name
	serverSubject        pkix.Name
	subjectPaddingHours  int
	certificateValidityY int
}

func NewGenerator(outputPath, extPath string, paths Paths, caSubjectDN, serverSubjectDN string, validityYears int) (*Generator, error) {
	if outputPath == "" {
		return nil, errMissingOutputPath
	}

	if extPath == "" {
		return nil, fmt.Errorf("cert: extension config path is not configured")
	}

	finalPaths, err := validatePaths(paths)
	if err != nil {
		return nil, err
	}

	finalCASubject, err := parseSubject(caSubjectDN, "CA")
	if err != nil {
		return nil, err
	}

	finalServerSubject, err := parseSubject(serverSubjectDN, "server")
	if err != nil {
		return nil, err
	}

	paddingHours := normalizePaddingHours(subjectPaddingHours)

	if validityYears <= 0 {
		return nil, fmt.Errorf("cert: certificate validity years must be positive")
	}

	return &Generator{
		outputPath:           outputPath,
		extPath:              extPath,
		paths:                finalPaths,
		caSubject:            finalCASubject,
		serverSubject:        finalServerSubject,
		subjectPaddingHours:  paddingHours,
		certificateValidityY: validityYears,
	}, nil
}

func validatePaths(p Paths) (Paths, error) {
	if p.CAKey == "" || p.CACert == "" || p.ServerKey == "" || p.ServerCSR == "" || p.ServerCert == "" || p.ServerFullChain == "" || p.AgentKey == "" || p.AgentCSR == "" || p.AgentCert == "" {
		return Paths{}, fmt.Errorf("cert: all Paths fields must be specified")
	}
	return p, nil
}

func parseSubject(subjectDN, label string) (pkix.Name, error) {
	subjectDN = strings.TrimSpace(subjectDN)
	if subjectDN == "" {
		return pkix.Name{}, fmt.Errorf("cert: %s subject string is empty", label)
	}

	name, err := subjectFromString(subjectDN)
	if err != nil {
		return pkix.Name{}, fmt.Errorf("cert: parse %s subject: %w", label, err)
	}

	if name.CommonName == "" {
		return pkix.Name{}, fmt.Errorf("cert: %s subject must define a common name", label)
	}

	return name, nil
}

func subjectFromString(subjectDN string) (pkix.Name, error) {
	subjectDN = strings.TrimSpace(subjectDN)
	if subjectDN == "" {
		return pkix.Name{}, fmt.Errorf("subject string is empty")
	}

	if strings.HasPrefix(subjectDN, "/") {
		subjectDN = subjectDN[1:]
	}

	parts := strings.Split(subjectDN, "/")
	var name pkix.Name
	var extras []pkix.AttributeTypeAndValue

	for _, part := range parts {
		if part == "" {
			continue
		}

		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return pkix.Name{}, fmt.Errorf("invalid subject component %q", part)
		}

		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])
		if key == "" {
			return pkix.Name{}, fmt.Errorf("subject component %q has empty key", part)
		}

		switch strings.ToLower(key) {
		case "c":
			if value != "" {
				name.Country = append(name.Country, value)
			}
		case "st":
			if value != "" {
				name.Province = append(name.Province, value)
			}
		case "l":
			if value != "" {
				name.Locality = append(name.Locality, value)
			}
		case "o":
			if value != "" {
				name.Organization = append(name.Organization, value)
			}
		case "ou":
			if value != "" {
				name.OrganizationalUnit = append(name.OrganizationalUnit, value)
			}
		case "cn":
			name.CommonName = value
		case "emailaddress":
			if value != "" {
				extras = append(extras, pkix.AttributeTypeAndValue{Type: emailAddressOID, Value: value})
			}
		default:
			return pkix.Name{}, fmt.Errorf("unsupported subject attribute %q", key)
		}
	}

	name.ExtraNames = append(name.ExtraNames, extras...)
	return name, nil
}

func normalizePaddingHours(padding int) int {
	if padding <= 0 {
		return 1
	}

	return padding
}

func (g *Generator) GenerateCAKey() error {
	if err := g.ensureOutputDir(); err != nil {
		return err
	}

	caKeyPath := g.join(g.paths.CAKey)
	if err := GeneratePrivateKey(caKeyPath); err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}

	return nil
}

func (g *Generator) ExistsCAKey() (bool, error) {
	return g.pathExists(g.paths.CAKey)
}

func (g *Generator) GenerateCACertificate() error {
	if err := g.ensureOutputDir(); err != nil {
		return err
	}

	caKeyPath := g.join(g.paths.CAKey)
	caCertPath := g.join(g.paths.CACert)

	privateKey, err := loadECPrivateKey(caKeyPath)
	if err != nil {
		return fmt.Errorf("load CA private key: %w", err)
	}

	caCertPEM, err := g.createCACertificatePEM(privateKey)
	if err != nil {
		return fmt.Errorf("create CA certificate: %w", err)
	}

	if err := saveCertificate(string(caCertPEM), caCertPath); err != nil {
		return fmt.Errorf("store CA certificate: %w", err)
	}

	return nil
}

func (g *Generator) ExistsCACertificate() (bool, error) {
	return g.pathExists(g.paths.CACert)
}

func (g *Generator) GenerateServerKey() error {
	if err := g.ensureOutputDir(); err != nil {
		return err
	}

	serverKeyPath := g.join(g.paths.ServerKey)
	if err := GeneratePrivateKey(serverKeyPath); err != nil {
		return fmt.Errorf("generate server key: %w", err)
	}

	return nil
}

func (g *Generator) ExistsServerKey() (bool, error) {
	return g.pathExists(g.paths.ServerKey)
}

func (g *Generator) GenerateServerCertificate() error {
	if err := g.ensureOutputDir(); err != nil {
		return err
	}

	serverKeyPath := g.join(g.paths.ServerKey)
	serverCSRPath := g.join(g.paths.ServerCSR)
	serverCertPath := g.join(g.paths.ServerCert)
	caKeyPath := g.join(g.paths.CAKey)
	caCertPath := g.join(g.paths.CACert)
	fullChainPath := g.join(g.paths.ServerFullChain)

	caKey, err := loadECPrivateKey(caKeyPath)
	if err != nil {
		return fmt.Errorf("load CA private key: %w", err)
	}

	caCert, caCertPEM, err := loadCertificate(caCertPath)
	if err != nil {
		return fmt.Errorf("load CA certificate: %w", err)
	}

	csrCommonName := g.serverSubject.CommonName
	csrPEM, err := CreateCSR(csrCommonName, serverKeyPath, serverCSRPath)
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

	serverCertPEM, err := g.createServerCertificatePEM(csr, caCert, caKey, dnsNames, ipAddresses)
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

func (g *Generator) ExistsServerCertificate() (bool, error) {
	return g.pathExists(g.paths.ServerCert)
}

func (g *Generator) GenerateFullchainCertificate() error {
	if err := g.ensureOutputDir(); err != nil {
		return err
	}

	serverCertPath := g.join(g.paths.ServerCert)
	caCertPath := g.join(g.paths.CACert)
	fullChainPath := g.join(g.paths.ServerFullChain)

	_, serverCertPEM, err := loadCertificate(serverCertPath)
	if err != nil {
		return fmt.Errorf("load server certificate: %w", err)
	}

	_, caCertPEM, err := loadCertificate(caCertPath)
	if err != nil {
		return fmt.Errorf("load CA certificate: %w", err)
	}

	if err := writeFullChain(fullChainPath, serverCertPEM, caCertPEM); err != nil {
		return err
	}

	return nil
}

func (g *Generator) ExistsFullchainCertificate() (bool, error) {
	return g.pathExists(g.paths.ServerFullChain)
}

func (g *Generator) GenerateAgentKey() error {
	if err := g.ensureOutputDir(); err != nil {
		return err
	}

	agentKeyPath := g.join(g.paths.AgentKey)
	if err := GeneratePrivateKey(agentKeyPath); err != nil {
		return fmt.Errorf("generate agent key: %w", err)
	}

	return nil
}

func (g *Generator) GenerateAgentCSR(certificateID string) error {
	if err := g.ensureOutputDir(); err != nil {
		return err
	}

	certificateID = strings.TrimSpace(certificateID)
	if certificateID == "" {
		return fmt.Errorf("cert: certificate ID is required")
	}

	agentKeyPath := g.join(g.paths.AgentKey)
	if _, err := os.Stat(agentKeyPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("cert: agent key does not exist at %s", agentKeyPath)
		}
		return fmt.Errorf("cert: check agent key: %w", err)
	}

	agentCSRPath := g.join(g.paths.AgentCSR)
	if _, err := CreateCSR(certificateID, agentKeyPath, agentCSRPath); err != nil {
		return fmt.Errorf("create agent CSR: %w", err)
	}

	return nil
}

func (g *Generator) GenerateAgentCertificate() (*time.Time, error) {
	if err := g.ensureOutputDir(); err != nil {
		return nil, err
	}

	caKeyPath := g.join(g.paths.CAKey)
	caCertPath := g.join(g.paths.CACert)
	agentCSRPath := g.join(g.paths.AgentCSR)
	agentCertPath := g.join(g.paths.AgentCert)

	caKey, err := loadECPrivateKey(caKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load CA private key: %w", err)
	}

	caCert, _, err := loadCertificate(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("load CA certificate: %w", err)
	}

	csrData, err := os.ReadFile(agentCSRPath)
	if err != nil {
		return nil, fmt.Errorf("read agent CSR: %w", err)
	}

	agentCertPEM, _, expiresAt, err := SignCSR(string(csrData), caCert, caKey, g.certificateValidityY)
	if err != nil {
		return nil, fmt.Errorf("sign agent CSR: %w", err)
	}

	if err := saveCertificate(agentCertPEM, agentCertPath); err != nil {
		return nil, fmt.Errorf("store agent certificate: %w", err)
	}

	return &expiresAt, nil
}

func (g *Generator) GetAgentCertificate() ([]byte, error) {
	agentCertPath := g.join(g.paths.AgentCert)
	data, err := os.ReadFile(agentCertPath)
	if err != nil {
		return nil, fmt.Errorf("read agent certificate: %w", err)
	}

	return data, nil
}

func (g *Generator) DeleteAgentKey() error {
	return g.deleteArtifact(g.paths.AgentKey, "agent private key")
}

func (g *Generator) DeleteAgentCSR() error {
	return g.deleteArtifact(g.paths.AgentCSR, "agent CSR")
}

func (g *Generator) DeleteAgentCertificate() error {
	return g.deleteArtifact(g.paths.AgentCert, "agent certificate")
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

func (g *Generator) pathExists(filename string) (bool, error) {
	fullPath := g.join(filename)
	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, fmt.Errorf("check %s: %w", fullPath, err)
	}

	return true, nil
}

func (g *Generator) deleteArtifact(filename, label string) error {
	fullPath := g.join(filename)
	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return fmt.Errorf("delete %s (%s): %w", label, fullPath, err)
	}

	return nil
}

func (g *Generator) createCACertificatePEM(privateKey *ecdsa.PrivateKey) ([]byte, error) {
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate CA serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               g.caSubject,
		NotBefore:             time.Now().Add(-g.subjectPadding()),
		NotAfter:              time.Now().AddDate(g.certificateValidityY, 0, 0),
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

func (g *Generator) createServerCertificatePEM(csr *x509.CertificateRequest, caCert *x509.Certificate, caKey crypto.Signer, dns []string, ips []net.IP) ([]byte, error) {
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate server serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               g.serverSubject,
		NotBefore:             time.Now().Add(-g.subjectPadding()),
		NotAfter:              time.Now().AddDate(g.certificateValidityY, 0, 0),
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

func (g *Generator) subjectPadding() time.Duration {
	return time.Duration(g.subjectPaddingHours) * time.Hour
}

func subjectKeyID(pub crypto.PublicKey) ([]byte, error) {
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("marshal public key: %w", err)
	}

	sum := sha1.Sum(pubKeyBytes)
	return sum[:], nil
}
