// Package pat generates and hashes personal access tokens.
//
// A token is "wfp_" + 40 base62 chars (~238 bits from crypto/rand). Only its
// SHA-256 is stored; the plaintext is shown to the user once at creation.
// The entropy makes a salt/KDF unnecessary — lookup is a unique-index hit on
// the hex digest.
package pat

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

const (
	// TokenPrefix marks WinterFlow PATs (helps secret scanners and lets the
	// auth middleware route Bearer tokens without a DB hit).
	TokenPrefix = "wfp_"
	// PrefixLen is how much of the plaintext is stored and shown in lists.
	PrefixLen = 12

	randomLen = 40
	alphabet  = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// Generate returns a new token's plaintext, its hex SHA-256, and its display
// prefix (the first PrefixLen characters).
func Generate() (plaintext, hash, prefix string, err error) {
	buf := make([]byte, randomLen)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", err
	}
	chars := make([]byte, randomLen)
	for i, b := range buf {
		chars[i] = alphabet[int(b)%len(alphabet)]
	}
	plaintext = TokenPrefix + string(chars)
	return plaintext, Hash(plaintext), plaintext[:PrefixLen], nil
}

// Hash returns the lowercase hex SHA-256 of a token plaintext.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
