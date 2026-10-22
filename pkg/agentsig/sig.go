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
	"fmt"
	"os"
	"strconv"
)

// Header carries the signature on a request.
const Header = "X-Winterflow-Signature"

// ErrInvalid: the signature does not verify against the certificate.
var ErrInvalid = errors.New("signature does not verify")

// Domain separates these signatures from anything else the agent key signs
// (the TLS handshake among them): it is the first line of every payload.
const Domain = "winterflow-agent-hub-v1"

// Payload is the signed form of a request: Domain, serverID, ts and nonce,
// each on its own line.
func Payload(serverID string, ts int64, nonce string) []byte {
	return []byte(Domain + "\n" + serverID + "\n" + strconv.FormatInt(ts, 10) + "\n" + nonce)
}

// LoadKey reads the agent's PEM private key (SEC 1 "EC PRIVATE KEY" or
// PKCS #8 "PRIVATE KEY" holding an ECDSA key).
func LoadKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("key is not PEM")
	}
	switch block.Type {
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		ec, ok := k.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("key is not ECDSA")
		}
		return ec, nil
	default:
		return nil, fmt.Errorf("unsupported key type %q", block.Type)
	}
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
