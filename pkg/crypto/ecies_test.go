package crypto

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
	"testing"
	"time"
)

// encryptForTest replicates the browser-side ECIES encryption (ephemeral P-256
// key + ECDH + SHA-256 + AES-256-GCM) so we can test DecryptWithPrivateKey
// without the browser.
func encryptForTest(t *testing.T, pub *ecdsa.PublicKey, plaintext string) string {
	t.Helper()
	curve := elliptic.P256()

	eph, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatalf("gen ephemeral key: %v", err)
	}
	sharedX, _ := curve.ScalarMult(pub.X, pub.Y, eph.D.Bytes())
	keyHash := sha256.Sum256(leftPad(sharedX.Bytes(), coordSize))

	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	iv := make([]byte, ivLen)
	if _, err := rand.Read(iv); err != nil {
		t.Fatalf("iv: %v", err)
	}
	ct := gcm.Seal(nil, iv, []byte(plaintext), nil)

	ephPoint := elliptic.Marshal(curve, eph.X, eph.Y) //nolint:staticcheck
	payload := append(append(append([]byte{}, ephPoint...), iv...), ct...)
	return base64.StdEncoding.EncodeToString(payload)
}

func writeKeyAndCert(t *testing.T, dir string) (keyPath, certPath string, pub *ecdsa.PublicKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPath = filepath.Join(dir, "agent.key")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-agent"},
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Unix(1<<31, 0),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPath = filepath.Join(dir, "agent.crt")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	return keyPath, certPath, &key.PublicKey
}

func TestECIESRoundTrip(t *testing.T) {
	dir := t.TempDir()
	keyPath, _, pub := writeKeyAndCert(t, dir)

	for _, secret := range []string{"hunter2", "", "a longer secret with spaces & symbols!@#"} {
		enc := encryptForTest(t, pub, secret)
		got, err := DecryptWithPrivateKey(keyPath, enc)
		if err != nil {
			t.Fatalf("decrypt %q: %v", secret, err)
		}
		if got != secret {
			t.Errorf("round trip = %q, want %q", got, secret)
		}
	}
}

func TestPublicKeyPointFromCertMatchesKey(t *testing.T) {
	dir := t.TempDir()
	keyPath, certPath, pub := writeKeyAndCert(t, dir)

	// The point exported from the cert must encrypt to something the private key
	// can decrypt — i.e. it really is the agent's public key.
	pointB64, err := PublicKeyPointFromCertPath(certPath)
	if err != nil {
		t.Fatalf("export pubkey: %v", err)
	}
	wantPoint := base64.StdEncoding.EncodeToString(elliptic.Marshal(pub.Curve, pub.X, pub.Y)) //nolint:staticcheck
	if pointB64 != wantPoint {
		t.Fatalf("exported point mismatch")
	}

	enc := encryptForTest(t, pub, "top-secret")
	got, err := DecryptWithPrivateKey(keyPath, enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != "top-secret" {
		t.Errorf("got %q", got)
	}
}

func TestDecryptRejectsShortPayload(t *testing.T) {
	dir := t.TempDir()
	keyPath, _, _ := writeKeyAndCert(t, dir)
	if _, err := DecryptWithPrivateKey(keyPath, base64.StdEncoding.EncodeToString([]byte("too short"))); err == nil {
		t.Fatal("expected error for short payload")
	}
}
