// Package crypto implements the ECIES (P-256) scheme WinterFlow uses to protect
// app secrets in transit and at rest.
//
// The browser encrypts a secret with the agent's EC public key and the agent
// decrypts it with its EC private key (its mTLS keypair). The wire format is a
// base64 string of:
//
//	[ ephemeral public key (65 bytes, 0x04 || X || Y) | IV (12) | ciphertext+tag ]
//
// The symmetric key is derived by ECDH between the ephemeral key and the
// agent's key, taking SHA-256 of the 32-byte big-endian X coordinate of the
// shared point, used as an AES-256-GCM key. This mirrors v1 exactly so existing
// secrets and the browser implementation interoperate.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
)

const (
	rawPubKeyLen = 65 // 0x04 || X(32) || Y(32) for P-256
	ivLen        = 12 // AES-GCM nonce
	coordSize    = 32 // P-256 coordinate size
	gcmTagLen    = 16
)

// DecryptWithPrivateKey decrypts a base64 ECIES payload using the EC (P-256)
// private key at keyPath. Returns the plaintext.
func DecryptWithPrivateKey(keyPath, encryptedBase64 string) (string, error) {
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return "", fmt.Errorf("read private key: %w", err)
	}
	priv, err := parseECPrivateKey(keyData)
	if err != nil {
		return "", err
	}
	return decrypt(priv, encryptedBase64)
}

func decrypt(ecKey *ecdsa.PrivateKey, encryptedBase64 string) (string, error) {
	encryptedData, err := base64.StdEncoding.DecodeString(encryptedBase64)
	if err != nil {
		return "", fmt.Errorf("decode base64 payload: %w", err)
	}

	if len(encryptedData) < rawPubKeyLen+ivLen+gcmTagLen {
		return "", fmt.Errorf("encrypted payload too short: got %d bytes", len(encryptedData))
	}

	rawPubKey := encryptedData[:rawPubKeyLen]
	iv := encryptedData[rawPubKeyLen : rawPubKeyLen+ivLen]
	ciphertext := encryptedData[rawPubKeyLen+ivLen:]

	if rawPubKey[0] != 0x04 {
		return "", fmt.Errorf("unexpected EC public key format: first byte 0x%02x, want 0x04", rawPubKey[0])
	}

	x := new(big.Int).SetBytes(rawPubKey[1 : 1+coordSize])
	y := new(big.Int).SetBytes(rawPubKey[1+coordSize : 1+2*coordSize])

	curve := elliptic.P256()
	if !curve.IsOnCurve(x, y) {
		return "", fmt.Errorf("ephemeral public key is not on the P-256 curve")
	}

	// ECDH: shared point = ephemeral_pub * agent_priv.
	sharedX, _ := curve.ScalarMult(x, y, ecKey.D.Bytes())
	if sharedX == nil {
		return "", fmt.Errorf("failed to derive shared secret")
	}

	keyHash := sha256.Sum256(leftPad(sharedX.Bytes(), coordSize))

	blockCipher, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return "", fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(blockCipher)
	if err != nil {
		return "", fmt.Errorf("create AES-GCM: %w", err)
	}
	plaintext, err := gcm.Open(nil, iv, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}

// PublicKeyPointFromCertPath reads an X.509 certificate (PEM) and returns its EC
// public key as the base64 of the uncompressed point (0x04 || X || Y). This is
// the form the browser needs to perform the matching ECIES encryption.
func PublicKeyPointFromCertPath(certPath string) (string, error) {
	pemData, err := os.ReadFile(certPath)
	if err != nil {
		return "", fmt.Errorf("read certificate: %w", err)
	}
	block, _ := pem.Decode(pemData)
	if block == nil {
		return "", fmt.Errorf("decode certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse certificate: %w", err)
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("certificate does not hold an EC public key")
	}
	point := elliptic.Marshal(pub.Curve, pub.X, pub.Y) //nolint:staticcheck // uncompressed point form is the wire contract
	return base64.StdEncoding.EncodeToString(point), nil
}

func parseECPrivateKey(pemData []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("decode private key PEM")
	}
	switch block.Type {
	case "EC PRIVATE KEY":
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse EC private key: %w", err)
		}
		return key, nil
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS8 private key: %w", err)
		}
		ec, ok := k.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not EC")
		}
		return ec, nil
	default:
		return nil, fmt.Errorf("unsupported private key type %q (want EC P-256)", block.Type)
	}
}

func leftPad(b []byte, size int) []byte {
	if len(b) >= size {
		return b
	}
	padded := make([]byte, size)
	copy(padded[size-len(b):], b)
	return padded
}
