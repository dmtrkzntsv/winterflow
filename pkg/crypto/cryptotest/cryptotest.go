// Package cryptotest is the browser's half of pkg/crypto's ECIES scheme, for
// tests of code that decrypts on the agent. Only _test files import it.
package cryptotest

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

// Encrypt mirrors the browser's ECIES: an ephemeral P-256 key, ECDH with
// pub, SHA-256 of the shared X coordinate (32 bytes, big-endian) as the
// AES-256-GCM key; the result is base64(ephemeral point | IV | ciphertext).
func Encrypt(t testing.TB, pub *ecdsa.PublicKey, plaintext string) string {
	t.Helper()
	curve := elliptic.P256()
	eph, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sharedX, _ := curve.ScalarMult(pub.X, pub.Y, eph.D.Bytes()) //nolint:staticcheck // mirrors the browser
	xb := make([]byte, 32)
	sharedX.FillBytes(xb)
	keyHash := sha256.Sum256(xb)
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	ct := gcm.Seal(nil, iv, []byte(plaintext), nil)
	ephPoint := elliptic.Marshal(curve, eph.X, eph.Y) //nolint:staticcheck // mirrors the browser
	payload := append(append(append([]byte{}, ephPoint...), iv...), ct...)
	return base64.StdEncoding.EncodeToString(payload)
}
