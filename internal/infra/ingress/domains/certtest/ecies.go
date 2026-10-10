package certtest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

// AgentKey writes a fresh P-256 agent.key into certDir (point HUB_CERT_DIR
// at it) and returns the public half for Encrypt.
func AgentKey(t testing.TB, certDir string) *ecdsa.PublicKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "agent.key"),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return &key.PublicKey
}

// Encrypt mirrors the browser's ECIES (ephemeral P-256 + ECDH + SHA-256 +
// AES-256-GCM), the scheme pkg/crypto decrypts.
func Encrypt(t testing.TB, pub *ecdsa.PublicKey, plaintext string) string {
	t.Helper()
	curve := elliptic.P256()
	eph, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sharedX, _ := curve.ScalarMult(pub.X, pub.Y, eph.D.Bytes()) //nolint:staticcheck // mirrors the browser
	xb := sharedX.Bytes()
	for len(xb) < 32 {
		xb = append([]byte{0}, xb...)
	}
	keyHash := sha256.Sum256(xb)
	block, _ := aes.NewCipher(keyHash[:])
	gcm, _ := cipher.NewGCM(block)
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	ct := gcm.Seal(nil, iv, []byte(plaintext), nil)
	ephPoint := elliptic.Marshal(curve, eph.X, eph.Y) //nolint:staticcheck // mirrors the browser
	payload := append(append(append([]byte{}, ephPoint...), iv...), ct...)
	return base64.StdEncoding.EncodeToString(payload)
}
