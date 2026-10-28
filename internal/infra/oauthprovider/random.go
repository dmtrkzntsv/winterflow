// Package oauthprovider implements the relying-party side of OpenID Connect
// with the standard library: discovery, the authorization-code request (with
// PKCE), the code exchange, ID-token verification against the issuer's JWKS,
// the userinfo fallback and Apple's signed client secret. It knows nothing
// about users or sessions. Ported from econumo's internal/infra/oidc.
package oauthprovider

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
)

// RandomToken returns 32 random bytes as unpadded base64url (43 chars), the
// alphabet Winterflow's access tokens use.
func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func Sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
