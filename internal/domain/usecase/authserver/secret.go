package authserver

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// newSecret makes a raw secret (32 random bytes, base64url) with prefix and
// its SHA-256 hex, the only form stored.
func newSecret(prefix string) (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	raw = prefix + base64.RawURLEncoding.EncodeToString(b)
	return raw, hashSecret(raw), nil
}

func hashSecret(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Prefixes of the raw secrets this server hands out: refresh tokens are
// wfr_ (Global Constraints); codes and client secrets carry none.
const refreshPrefix = "wfr_"
