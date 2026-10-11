package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
	"winterflow/pkg/crypto/cryptotest"
)

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
		enc := cryptotest.Encrypt(t, pub, secret)
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

	enc := cryptotest.Encrypt(t, pub, "top-secret")
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
