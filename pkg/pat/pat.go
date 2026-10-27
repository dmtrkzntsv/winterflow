// Package pat holds the personal access token's marks and the hash every
// access token is stored by.
//
// A PAT is "wfp_" and random characters (internal/domain/usecase/authn mints
// them; tokens from before it are 40 base62 characters). Only its SHA-256 is
// stored; the plaintext is shown to the user once at creation. The entropy
// makes a salt/KDF unnecessary — lookup is a unique-index hit on the hex
// digest.
package pat

import (
	"crypto/sha256"
	"encoding/hex"
)

const (
	// TokenPrefix marks WinterFlow PATs (helps secret scanners and lets the
	// auth middleware route Bearer tokens without a DB hit).
	TokenPrefix = "wfp_"
	// PrefixLen is how much of the plaintext is stored and shown in lists.
	PrefixLen = 12
)

// Hash returns the lowercase hex SHA-256 of a token plaintext.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
