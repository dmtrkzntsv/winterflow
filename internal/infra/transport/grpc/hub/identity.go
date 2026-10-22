package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// peerIdentity reads the connecting agent's identity from its verified mTLS
// client certificate: the CN is the server id, the fingerprint the lowercase
// hex SHA-256 of the certificate's DER (the same form as cert.Fingerprint).
func peerIdentity(ctx context.Context) (serverID, fingerprint string, err error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", "", errors.New("no peer in context")
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", "", errors.New("peer is not authenticated by TLS")
	}
	if len(info.State.VerifiedChains) == 0 || len(info.State.VerifiedChains[0]) == 0 {
		return "", "", errors.New("no verified client certificate")
	}
	leaf := info.State.VerifiedChains[0][0]
	if leaf.Subject.CommonName == "" {
		return "", "", errors.New("client certificate has no common name")
	}
	sum := sha256.Sum256(leaf.Raw)
	return leaf.Subject.CommonName, hex.EncodeToString(sum[:]), nil
}
