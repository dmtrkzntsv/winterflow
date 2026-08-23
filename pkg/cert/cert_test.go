package cert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
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

func TestGeneratePrivateKey(t *testing.T) {
	dir := t.TempDir()
	// The parent directory does not exist yet — GeneratePrivateKey must create it.
	keyPath := filepath.Join(dir, "nested", "sub", "test.key")
	if err := GeneratePrivateKey(keyPath); err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("key file mode = %o, want 600", perm)
		}
	}

	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		t.Fatalf("expected EC PRIVATE KEY PEM block, got %v", block)
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	if key.Curve != elliptic.P256() {
		t.Errorf("curve = %s, want P-256", key.Curve.Params().Name)
	}
}

func TestCreateCSR(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "agent.key")
	if err := GeneratePrivateKey(keyPath); err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}

	csrPath := filepath.Join(dir, "out", "agent.csr")
	csrPEM, err := CreateCSR("agent-42", keyPath, csrPath)
	if err != nil {
		t.Fatalf("CreateCSR: %v", err)
	}

	// The returned string and the file must both contain the same valid CSR.
	fileData, err := os.ReadFile(csrPath)
	if err != nil {
		t.Fatalf("read CSR file: %v", err)
	}
	if string(fileData) != csrPEM {
		t.Error("returned CSR PEM differs from file content")
	}

	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		t.Fatalf("expected CERTIFICATE REQUEST PEM block")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatalf("parse CSR: %v", err)
	}
	if csr.Subject.CommonName != "agent-42" {
		t.Errorf("CSR CommonName = %q, want agent-42", csr.Subject.CommonName)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Errorf("CSR signature invalid: %v", err)
	}
}

func TestCreateCSRErrors(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing key file", func(t *testing.T) {
		if _, err := CreateCSR("id", filepath.Join(dir, "nope.key"), filepath.Join(dir, "out.csr")); err == nil {
			t.Fatal("expected error for missing key")
		}
	})

	t.Run("not PEM", func(t *testing.T) {
		p := filepath.Join(dir, "junk.key")
		if err := os.WriteFile(p, []byte("not a pem"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := CreateCSR("id", p, filepath.Join(dir, "out.csr")); err == nil {
			t.Fatal("expected error for non-PEM key")
		}
	})

	t.Run("wrong PEM type", func(t *testing.T) {
		p := filepath.Join(dir, "wrong-type.key")
		pemData := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{0x01}})
		if err := os.WriteFile(p, pemData, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := CreateCSR("id", p, filepath.Join(dir, "out.csr"))
		if err == nil || !strings.Contains(err.Error(), "unsupported private key type") {
			t.Fatalf("expected unsupported key type error, got %v", err)
		}
	})

	t.Run("wrong curve", func(t *testing.T) {
		key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		der, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "p384.key")
		if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = CreateCSR("id", p, filepath.Join(dir, "out.csr"))
		if err == nil || !strings.Contains(err.Error(), "P-256") {
			t.Fatalf("expected P-256 curve error, got %v", err)
		}
	})
}

func TestSignCSR(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey, _, _ := selfSignedCA(t, dir)

	leafKeyPath := filepath.Join(dir, "leaf.key")
	if err := GeneratePrivateKey(leafKeyPath); err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	csrPEM, err := CreateCSR("leaf-cn", leafKeyPath, filepath.Join(dir, "leaf.csr"))
	if err != nil {
		t.Fatalf("CreateCSR: %v", err)
	}

	const validity = 2
	certPEM, commonName, expiresAt, err := SignCSR(csrPEM, caCert, caKey, validity)
	if err != nil {
		t.Fatalf("SignCSR: %v", err)
	}
	if commonName != "leaf-cn" {
		t.Errorf("commonName = %q, want leaf-cn", commonName)
	}

	wantExpiry := time.Now().AddDate(validity, 0, 0)
	if diff := expiresAt.Sub(wantExpiry); diff < -time.Minute || diff > time.Minute {
		t.Errorf("expiresAt = %v, want ~%v", expiresAt, wantExpiry)
	}

	block, _ := pem.Decode([]byte(certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatal("expected CERTIFICATE PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse signed cert: %v", err)
	}
	if cert.Subject.CommonName != "leaf-cn" {
		t.Errorf("cert CommonName = %q, want leaf-cn", cert.Subject.CommonName)
	}
	if err := cert.CheckSignatureFrom(caCert); err != nil {
		t.Errorf("cert not signed by CA: %v", err)
	}
	// DER NotAfter has 1-second granularity.
	if diff := cert.NotAfter.Sub(expiresAt); diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("cert NotAfter %v != returned expiry %v", cert.NotAfter, expiresAt)
	}
}

func TestSignCSRErrors(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey, _, _ := selfSignedCA(t, dir)

	leafKeyPath := filepath.Join(dir, "leaf.key")
	if err := GeneratePrivateKey(leafKeyPath); err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	csrPEM, err := CreateCSR("leaf-cn", leafKeyPath, filepath.Join(dir, "leaf.csr"))
	if err != nil {
		t.Fatalf("CreateCSR: %v", err)
	}

	if _, _, _, err := SignCSR(csrPEM, nil, caKey, 1); err == nil {
		t.Error("expected error for nil CA certificate")
	}
	if _, _, _, err := SignCSR(csrPEM, caCert, nil, 1); err == nil {
		t.Error("expected error for nil CA key")
	}
	if _, _, _, err := SignCSR(csrPEM, caCert, caKey, 0); err == nil {
		t.Error("expected error for zero validity")
	}
	if _, _, _, err := SignCSR("not a pem", caCert, caKey, 1); err == nil {
		t.Error("expected error for non-PEM CSR")
	}
	junk := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: []byte{0xde, 0xad, 0xbe, 0xef}})
	if _, _, _, err := SignCSR(string(junk), caCert, caKey, 1); err == nil {
		t.Error("expected error for garbage CSR DER")
	}
}
