package cert

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// selfSignedCA generates an EC P-256 CA on disk and returns the parsed
// certificate, the signer key and the file paths.
func selfSignedCA(t *testing.T, dir string) (*x509.Certificate, *ecdsa.PrivateKey, string, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen CA key: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal CA key: %v", err)
	}
	keyPath := filepath.Join(dir, "ca.key")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write CA key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "unit-test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	certPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), 0o644); err != nil {
		t.Fatalf("write CA cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	return caCert, key, certPath, keyPath
}

func csrFor(t *testing.T, cn string, key crypto.Signer) string {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}}, key)
	if err != nil {
		t.Fatalf("csr: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func TestSignCSRAsOverridesSubject(t *testing.T) {
	caCert, caKey, _, _ := selfSignedCA(t, t.TempDir())
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	certPEM, exp, err := SignCSRAs(csrFor(t, "evil", key), "server-123", caCert, caKey, 5)
	if err != nil {
		t.Fatalf("SignCSRAs: %v", err)
	}
	if exp.After(caCert.NotAfter) {
		t.Errorf("expiresAt %v outlives the CA %v", exp, caCert.NotAfter)
	}
	block, _ := pem.Decode([]byte(certPEM))
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject.CommonName != "server-123" || len(c.Subject.Organization) != 0 {
		t.Errorf("subject = %v", c.Subject)
	}
	if len(c.ExtKeyUsage) != 1 || c.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Errorf("ExtKeyUsage = %v", c.ExtKeyUsage)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := c.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Errorf("verify: %v", err)
	}
}

func TestSignCSRAsRejectsBadCSR(t *testing.T) {
	caCert, caKey, _, _ := selfSignedCA(t, t.TempDir())
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	good := csrFor(t, "x", ec)

	block, _ := pem.Decode([]byte(good))
	tampered := append([]byte(nil), block.Bytes...)
	tampered[len(tampered)-5] ^= 0xff
	tamperedPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: tampered}))

	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)

	cases := map[string]string{
		"garbage":  "not a pem",
		"tampered": tamperedPEM,
		"rsa":      csrFor(t, "x", rsaKey),
		"p384":     csrFor(t, "x", p384),
	}
	for name, csr := range cases {
		if _, _, err := SignCSRAs(csr, "sid", caCert, caKey, 5); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, _, err := SignCSRAs(good, "", caCert, caKey, 5); err == nil {
		t.Error("empty common name: expected error")
	}
}

func TestFingerprintStable(t *testing.T) {
	caCert, caKey, _, _ := selfSignedCA(t, t.TempDir())
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	certPEM, _, err := SignCSRAs(csrFor(t, "x", key), "sid", caCert, caKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Fingerprint(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Fingerprint(certPEM)
	if a != b || len(a) != 64 || a != strings.ToLower(a) {
		t.Errorf("fingerprint %q / %q", a, b)
	}
	if _, err := Fingerprint("junk"); err == nil {
		t.Error("expected error for junk")
	}
}

func TestSignHubCSR(t *testing.T) {
	caCert, caKey, _, _ := selfSignedCA(t, t.TempDir())
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	certPEM, exp, err := SignHubCSR(csrFor(t, "evil", key), "hub-01.example.com", []string{"hub-01.example.com", "203.0.113.7"}, caCert, caKey, 2)
	if err != nil {
		t.Fatalf("SignHubCSR: %v", err)
	}
	block, _ := pem.Decode([]byte(certPEM))
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject.CommonName != "hub-01.example.com" {
		t.Errorf("CN = %q", c.Subject.CommonName)
	}
	if len(c.DNSNames) != 1 || c.DNSNames[0] != "hub-01.example.com" {
		t.Errorf("DNSNames = %v", c.DNSNames)
	}
	if len(c.IPAddresses) != 1 || c.IPAddresses[0].String() != "203.0.113.7" {
		t.Errorf("IPAddresses = %v", c.IPAddresses)
	}
	if len(c.ExtKeyUsage) != 1 || c.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("ExtKeyUsage = %v", c.ExtKeyUsage)
	}
	if d := exp.Sub(c.NotAfter); d < 0 || d > time.Second {
		t.Errorf("expiry %v != NotAfter %v", exp, c.NotAfter)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := c.Verify(x509.VerifyOptions{Roots: pool, DNSName: "hub-01.example.com", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Errorf("does not verify: %v", err)
	}
}

func TestSignHubCSRRejects(t *testing.T) {
	caCert, caKey, _, _ := selfSignedCA(t, t.TempDir())
	p256, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	good := csrFor(t, "x", p256)
	for name, f := range map[string]func() error{
		"p384": func() error {
			_, _, err := SignHubCSR(csrFor(t, "x", p384), "h", []string{"h"}, caCert, caKey, 1)
			return err
		},
		"garbage":  func() error { _, _, err := SignHubCSR("nope", "h", []string{"h"}, caCert, caKey, 1); return err },
		"no hosts": func() error { _, _, err := SignHubCSR(good, "h", nil, caCert, caKey, 1); return err },
		"no id":    func() error { _, _, err := SignHubCSR(good, "", []string{"h"}, caCert, caKey, 1); return err },
		"no years": func() error { _, _, err := SignHubCSR(good, "h", []string{"h"}, caCert, caKey, 0); return err },
		"no ca":    func() error { _, _, err := SignHubCSR(good, "h", []string{"h"}, nil, nil, 1); return err },
	} {
		if f() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
