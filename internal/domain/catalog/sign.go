package catalog

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// ErrSignature marks a feed whose signature does not verify: the sync
// refuses the whole feed.
var ErrSignature = errors.New("the catalog signature does not verify")

// ParsePublicKey reads a public key: 32 bytes, standard base64.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("catalog public key: want %d bytes of standard base64", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}

// ParsePrivateKey reads a signing key, standard base64: the 32-byte seed
// (what keygen prints) or the 64-byte private key.
func ParsePrivateKey(s string) (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	switch {
	case err != nil:
	case len(b) == ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(b), nil
	case len(b) == ed25519.PrivateKeySize:
		k := ed25519.PrivateKey(b)
		if bytes.Equal(ed25519.NewKeyFromSeed(k.Seed()), k) {
			return k, nil
		}
	}
	return nil, fmt.Errorf("catalog signing key: want a %d-byte seed or a %d-byte key, standard base64", ed25519.SeedSize, ed25519.PrivateKeySize)
}

// GenerateKey makes a key pair from random: the public key and the seed,
// each standard base64.
func GenerateKey(random io.Reader) (public, seed string, err error) {
	pub, priv, err := ed25519.GenerateKey(random)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv.Seed()), nil
}

// Sign is catalog.json.sig for feed, the exact bytes of catalog.json: the
// ed25519 signature in standard base64, then a newline.
func Sign(key ed25519.PrivateKey, feed []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, feed)) + "\n")
}

// Verify checks sig (a .sig file: surrounding whitespace is ignored)
// against the exact bytes of feed.
func Verify(key ed25519.PublicKey, feed, sig []byte) error {
	raw, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize || !ed25519.Verify(key, feed, raw) {
		return ErrSignature
	}
	return nil
}
