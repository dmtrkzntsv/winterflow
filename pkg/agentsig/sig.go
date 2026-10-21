// Package agentsig signs and verifies an agent's requests to the API with
// the agent's own certificate key: ECDSA over SHA-256 of the payload, the
// signature base64 of its ASN.1 form. Shared by the agent and the API.
package agentsig

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strconv"
)

// Header carries the signature on a request.
const Header = "X-Winterflow-Signature"

// ErrInvalid: the signature does not verify against the certificate.
var ErrInvalid = errors.New("signature does not verify")

// Payload is the signed form of a request: serverID, ts and nonce, each on
// its own line.
func Payload(serverID string, ts int64, nonce string) []byte {
	return []byte(serverID + "\n" + strconv.FormatInt(ts, 10) + "\n" + nonce)
}

// Sign signs payload with key.
func Sign(key *ecdsa.PrivateKey, payload []byte) (string, error) {
	sum := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// Verify checks sig over payload against the ECDSA key of the PEM
// certificate certPEM. Any failure (an unreadable certificate, a key that is
// not ECDSA, a malformed or wrong signature) is ErrInvalid.
func Verify(certPEM string, payload []byte, sig string) error {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return ErrInvalid
	}
	crt, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ErrInvalid
	}
	pub, ok := crt.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return ErrInvalid
	}
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return ErrInvalid
	}
	sum := sha256.Sum256(payload)
	if !ecdsa.VerifyASN1(pub, sum[:], raw) {
		return ErrInvalid
	}
	return nil
}
