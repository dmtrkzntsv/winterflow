// Package hubjoin issues the one-time tokens a new hub redeems for its
// server certificate (CN = hub id, SAN = its public host).
package hubjoin

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"time"

	"winterflow/internal/domain/port"
)

const (
	// TokenPrefix marks a hub join token.
	TokenPrefix = "wfh_"
	// DefaultTTL is how long an issued token stays redeemable.
	DefaultTTL = time.Hour
	// pruneAfter is how long a used or expired token row is kept.
	pruneAfter = 24 * time.Hour
	maxHost    = 253
)

var (
	// ErrInvalidToken covers an unknown, used and expired token alike, so
	// the answer is no oracle.
	ErrInvalidToken = errors.New("invalid or expired hub join token")
	ErrInvalidCSR   = errors.New("invalid certificate request")
	ErrInvalidAddr  = errors.New("invalid public address, want host:port")
)

// Signer issues hub certificates from the control plane's CA.
type Signer interface {
	SignHub(csrPEM, hubID string, hosts []string) (certPEM string, expiresAt time.Time, err error)
	CACertPEM() string
}

// UseCase issues and redeems hub join tokens.
type UseCase struct {
	repo   port.HubTokenRepository
	signer Signer
	now    func() time.Time
}

// New builds the use case; a nil now means time.Now.
func New(repo port.HubTokenRepository, signer Signer, now func() time.Time) *UseCase {
	if now == nil {
		now = time.Now
	}
	return &UseCase{repo: repo, signer: signer, now: now}
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// Issue creates a token valid for ttl (DefaultTTL when ttl <= 0). It is
// returned once; only its hash is stored. Rows a day past expiry or use are
// pruned on the way.
func (u *UseCase) Issue(ctx context.Context, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	now := u.now()
	if err := u.repo.Prune(ctx, now.Add(-pruneAfter)); err != nil {
		return "", time.Time{}, fmt.Errorf("prune hub tokens: %w", err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("hub token: %w", err)
	}
	token := TokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	expires := now.Add(ttl)
	if err := u.repo.Create(ctx, hashToken(token), expires, now); err != nil {
		return "", time.Time{}, fmt.Errorf("store hub token: %w", err)
	}
	return token, expires, nil
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// hubHost validates publicAddr (host:port) and returns its host, which is
// the hub id.
func hubHost(publicAddr string) (string, error) {
	host, p, err := net.SplitHostPort(publicAddr)
	if err != nil || host == "" || len(host) > maxHost {
		return "", ErrInvalidAddr
	}
	if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
		return "", ErrInvalidAddr
	}
	if net.ParseIP(host) == nil && !hostRe.MatchString(host) {
		return "", ErrInvalidAddr
	}
	return host, nil
}

func validateCSR(csrPEM string) error {
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil {
		return fmt.Errorf("%w: not PEM", ErrInvalidCSR)
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCSR, err)
	}
	if err := csr.CheckSignature(); err != nil {
		return fmt.Errorf("%w: bad signature", ErrInvalidCSR)
	}
	if pub, ok := csr.PublicKey.(*ecdsa.PublicKey); !ok || pub.Curve != elliptic.P256() {
		return fmt.Errorf("%w: key must be ECDSA P-256", ErrInvalidCSR)
	}
	return nil
}

// Redeem consumes the token and signs the hub's CSR for the host of
// publicAddr, which is also the hub id. The address and CSR are checked
// before the token is spent, so a typo does not burn it.
func (u *UseCase) Redeem(ctx context.Context, token, csrPEM, publicAddr string) (certPEM, caPEM string, err error) {
	hubID, err := hubHost(publicAddr)
	if err != nil {
		return "", "", err
	}
	if err := validateCSR(csrPEM); err != nil {
		return "", "", err
	}
	ok, err := u.repo.Consume(ctx, hashToken(token), hubID, u.now())
	if err != nil {
		return "", "", fmt.Errorf("redeem hub token: %w", err)
	}
	if !ok {
		return "", "", ErrInvalidToken
	}
	certPEM, _, err = u.signer.SignHub(csrPEM, hubID, []string{hubID})
	if err != nil {
		return "", "", fmt.Errorf("sign hub certificate: %w", err)
	}
	return certPEM, u.signer.CACertPEM(), nil
}
