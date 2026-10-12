// Package secretbox encrypts small secrets at rest (notification channel
// URLs) with AES-256-GCM under a key derived from a server secret
// (JWT_SECRET) by HKDF-SHA256. Rotating the server secret makes existing
// ciphertexts unreadable: they must be re-entered.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// info separates this key from any other use of the same server secret.
const info = "winterflow notification channel urls v1"

var (
	// ErrNoSecret is returned by New for an empty server secret.
	ErrNoSecret = errors.New("secretbox: empty secret")
	// ErrCorrupt is returned by Open for a ciphertext it cannot read:
	// tampered, truncated, or sealed under another secret.
	ErrCorrupt = errors.New("cannot decrypt the stored URL (was JWT_SECRET changed?); re-enter it")
)

// Box seals and opens strings. Safe for concurrent use.
type Box struct {
	aead cipher.AEAD
}

// New derives the box key from secret.
func New(secret string) (*Box, error) {
	if secret == "" {
		return nil, ErrNoSecret
	}
	key, err := hkdf.Key(sha256.New, []byte(secret), nil, info, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plain into base64(nonce || ciphertext).
func (b *Box) Seal(plain string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b.aead.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// Open decrypts what Seal produced.
func (b *Box) Open(sealed string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < b.aead.NonceSize()+b.aead.Overhead() {
		return "", ErrCorrupt
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", ErrCorrupt
	}
	return string(plain), nil
}
