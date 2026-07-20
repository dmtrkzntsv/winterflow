package cert

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// saveCertificate saves the certificate data to the specified path
func saveCertificate(certData, certPath string) error {
	// Create directory if it doesn't exist
	dir := filepath.Dir(certPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory for certificate: %v", err)
	}

	// Create file
	certFile, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create certificate file: %v", err)
	}
	defer certFile.Close()

	// Write certificate data to file
	if _, err := certFile.WriteString(certData); err != nil {
		return fmt.Errorf("failed to write certificate to file: %v", err)
	}

	return nil
}

func loadSubjectAltNames(extPath string) ([]string, []net.IP, error) {
	file, err := os.Open(extPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open extensions file: %w", err)
	}
	defer file.Close()

	var dns []string
	var ips []net.IP

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch {
		case strings.HasPrefix(key, "DNS."):
			dns = append(dns, value)
		case strings.HasPrefix(key, "IP."):
			ip := net.ParseIP(value)
			if ip == nil {
				return nil, nil, fmt.Errorf("invalid IP address %q in %s", value, extPath)
			}
			ips = append(ips, ip)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("read extensions file: %w", err)
	}

	return dns, ips, nil
}

func loadECPrivateKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("decode private key PEM")
	}

	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse EC private key: %w", err)
	}

	if key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("private key must be generated with prime256v1")
	}

	return key, nil
}

func loadCertificate(path string) (*x509.Certificate, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read certificate: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, nil, fmt.Errorf("decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse certificate: %w", err)
	}

	return cert, data, nil
}

func parseCSR(csrPEM string) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil {
		return nil, fmt.Errorf("decode CSR PEM")
	}

	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CSR: %w", err)
	}

	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("invalid CSR signature: %w", err)
	}

	return csr, nil
}

func writeFullChain(dest string, serverPEM, caPEM []byte) error {
	var buf bytes.Buffer

	buf.Write(serverPEM)
	if len(serverPEM) == 0 || serverPEM[len(serverPEM)-1] != '\n' {
		buf.WriteByte('\n')
	}

	buf.Write(caPEM)
	if len(caPEM) == 0 || caPEM[len(caPEM)-1] != '\n' {
		buf.WriteByte('\n')
	}

	if err := os.WriteFile(dest, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write full-chain certificate: %w", err)
	}

	return nil
}
