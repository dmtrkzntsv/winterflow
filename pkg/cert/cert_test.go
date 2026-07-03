package cert

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
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

func TestLoadTLSCredentials(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey, caCertPath, _ := selfSignedCA(t, dir)

	// Issue a server certificate for the key at serverKeyPath.
	serverKeyPath := filepath.Join(dir, "server.key")
	if err := GeneratePrivateKey(serverKeyPath); err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	csrPEM, err := CreateCSR("localhost", serverKeyPath, filepath.Join(dir, "server.csr"))
	if err != nil {
		t.Fatalf("CreateCSR: %v", err)
	}
	certPEM, _, _, err := SignCSR(csrPEM, caCert, caKey, 1)
	if err != nil {
		t.Fatalf("SignCSR: %v", err)
	}
	serverCertPath := filepath.Join(dir, "server.crt")
	if err := os.WriteFile(serverCertPath, []byte(certPEM), 0o600); err != nil {
		t.Fatalf("write server cert: %v", err)
	}

	for _, host := range []string{"localhost", "127.0.0.1", ""} {
		creds, err := LoadTLSCredentials(caCertPath, serverCertPath, serverKeyPath, host)
		if err != nil {
			t.Fatalf("LoadTLSCredentials(host=%q): %v", host, err)
		}
		if creds == nil {
			t.Fatalf("LoadTLSCredentials(host=%q) returned nil credentials", host)
		}
		if proto := creds.Info().SecurityProtocol; proto != "tls" {
			t.Errorf("security protocol = %q, want tls", proto)
		}
	}

	t.Run("missing key pair", func(t *testing.T) {
		if _, err := LoadTLSCredentials(caCertPath, filepath.Join(dir, "nope.crt"), serverKeyPath, "localhost"); err == nil {
			t.Fatal("expected error for missing certificate")
		}
	})

	t.Run("missing CA file", func(t *testing.T) {
		if _, err := LoadTLSCredentials(filepath.Join(dir, "nope-ca.crt"), serverCertPath, serverKeyPath, "localhost"); err == nil {
			t.Fatal("expected error for missing CA file")
		}
	})

	t.Run("invalid CA PEM", func(t *testing.T) {
		badCA := filepath.Join(dir, "bad-ca.crt")
		if err := os.WriteFile(badCA, []byte("not a certificate"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadTLSCredentials(badCA, serverCertPath, serverKeyPath, "localhost"); err == nil {
			t.Fatal("expected error for non-PEM CA file")
		}
	})
}

// eciesEncrypt replicates the browser-side ECIES scheme (ephemeral P-256 key +
// ECDH + SHA-256 + AES-256-GCM) so DecryptWithPrivateKey can be tested without
// the browser.
func eciesEncrypt(t *testing.T, pub *ecdsa.PublicKey, plaintext string) string {
	t.Helper()
	curve := elliptic.P256()

	eph, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatalf("gen ephemeral key: %v", err)
	}
	sharedX, _ := curve.ScalarMult(pub.X, pub.Y, eph.D.Bytes())
	shared := sharedX.Bytes()
	if len(shared) < 32 {
		padded := make([]byte, 32)
		copy(padded[32-len(shared):], shared)
		shared = padded
	}
	keyHash := sha256.Sum256(shared)

	blockCipher, err := aes.NewCipher(keyHash[:])
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	gcm, err := cipher.NewGCM(blockCipher)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		t.Fatalf("iv: %v", err)
	}
	ct := gcm.Seal(nil, iv, []byte(plaintext), nil)

	ephPoint := elliptic.Marshal(curve, eph.X, eph.Y) //nolint:staticcheck
	payload := append(append(append([]byte{}, ephPoint...), iv...), ct...)
	return base64.StdEncoding.EncodeToString(payload)
}

func TestDecryptWithPrivateKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "agent.key")
	if err := GeneratePrivateKey(keyPath); err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	key, err := loadECPrivateKey(keyPath)
	if err != nil {
		t.Fatalf("load key: %v", err)
	}

	for _, secret := range []string{"hunter2", "", "with spaces & symbols!@#"} {
		enc := eciesEncrypt(t, &key.PublicKey, secret)
		got, err := DecryptWithPrivateKey(keyPath, enc)
		if err != nil {
			t.Fatalf("decrypt %q: %v", secret, err)
		}
		if got != secret {
			t.Errorf("round trip = %q, want %q", got, secret)
		}
	}

	t.Run("short payload", func(t *testing.T) {
		if _, err := DecryptWithPrivateKey(keyPath, base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
			t.Fatal("expected error for short payload")
		}
	})

	t.Run("not base64", func(t *testing.T) {
		if _, err := DecryptWithPrivateKey(keyPath, "%%%not-base64%%%"); err == nil {
			t.Fatal("expected error for invalid base64")
		}
	})

	t.Run("compressed point prefix", func(t *testing.T) {
		payload := make([]byte, 65+12+16)
		payload[0] = 0x02 // compressed form, unsupported
		if _, err := DecryptWithPrivateKey(keyPath, base64.StdEncoding.EncodeToString(payload)); err == nil {
			t.Fatal("expected error for non-uncompressed public key")
		}
	})

	t.Run("missing key", func(t *testing.T) {
		if _, err := DecryptWithPrivateKey(filepath.Join(dir, "nope.key"), "aGk="); err == nil {
			t.Fatal("expected error for missing key file")
		}
	})
}

func TestSignWithPrivateKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "agent.key")
	if err := GeneratePrivateKey(keyPath); err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	key, err := loadECPrivateKey(keyPath)
	if err != nil {
		t.Fatalf("load key: %v", err)
	}

	msg := []byte("challenge-payload")
	sigB64, err := SignWithPrivateKey(keyPath, msg)
	if err != nil {
		t.Fatalf("SignWithPrivateKey: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		t.Fatalf("signature is not base64: %v", err)
	}
	hash := sha256.Sum256(msg)
	if !ecdsa.VerifyASN1(&key.PublicKey, hash[:], sig) {
		t.Fatal("signature does not verify against the public key")
	}
	// A signature over different data must not verify.
	otherHash := sha256.Sum256([]byte("tampered"))
	if ecdsa.VerifyASN1(&key.PublicKey, otherHash[:], sig) {
		t.Fatal("signature unexpectedly verifies tampered message")
	}

	if _, err := SignWithPrivateKey(filepath.Join(dir, "nope.key"), msg); err == nil {
		t.Fatal("expected error for missing key file")
	}
	badKey := filepath.Join(dir, "bad.key")
	if err := os.WriteFile(badKey, []byte("not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SignWithPrivateKey(badKey, msg); err == nil {
		t.Fatal("expected error for non-PEM key file")
	}
}
