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
	"strings"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	certpkg "winterflow/pkg/cert"
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
	// ErrHubLive: a live hub already uses the requested hub id, so a token
	// holder cannot take it over. It is an ErrInvalidAddr (a 400).
	ErrHubLive = fmt.Errorf("%w: a live hub already uses this host", ErrInvalidAddr)
	// ErrHubIDNotUser: the host cannot name a Redis user (only letters,
	// digits, dots and hyphens; an IPv6 address is refused). An
	// ErrInvalidAddr (a 400).
	ErrHubIDNotUser = fmt.Errorf("%w: the host must be a DNS name or an IPv4 address", ErrInvalidAddr)
)

// HubLister is the slice of port.HubRegistry Redeem needs.
type HubLister interface {
	List(ctx context.Context) ([]model.Hub, error)
}

// Signer issues hub certificates from the control plane's CA.
type Signer interface {
	SignHub(csrPEM, hubID string, hosts []string) (certPEM string, expiresAt time.Time, err error)
	CACertPEM() string
}

// CertTruster records an issued hub certificate as trusted (the Trust
// slice of port.HubCertRepository).
type CertTruster interface {
	Trust(ctx context.Context, hubID, fingerprint string, now time.Time) error
}

var _ CertTruster = port.HubCertRepository(nil)

// UseCase issues and redeems hub join tokens.
type UseCase struct {
	repo   port.HubTokenRepository
	certs  CertTruster
	users  port.HubRedisUsers
	signer Signer
	live   HubLister
	now    func() time.Time
}

// Joined is what a redeemed token gives the new hub: its certificate, the
// CA, and the Redis user it connects as (the password is never logged).
type Joined struct {
	CertPEM       string
	CAPEM         string
	RedisUsername string
	RedisPassword string
}

// New builds the use case. certs records each issued certificate as
// trusted, users creates the hub's Redis user, and live is the hub list
// Redeem checks the new hub's id against; all three are nil only where
// nothing is redeemed (the hub-token subcommand: Redeem then refuses every
// request). A nil now means time.Now.
func New(repo port.HubTokenRepository, certs CertTruster, users port.HubRedisUsers, signer Signer, live HubLister, now func() time.Time) *UseCase {
	if now == nil {
		now = time.Now
	}
	return &UseCase{repo: repo, certs: certs, users: users, signer: signer, live: live, now: now}
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

// hubHost validates publicAddr (host:port) and returns its host in lower
// case, which is the hub id (host names are case-insensitive; the hub
// derives the same id, config.GetHubID).
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
	return strings.ToLower(host), nil
}

// checkNotLive refuses a hub id a live hub already announces.
func (u *UseCase) checkNotLive(ctx context.Context, hubID string) error {
	if u.live == nil {
		return errors.New("no hub list to check the hub id against")
	}
	listed, err := u.live.List(ctx)
	if err != nil {
		return fmt.Errorf("list hubs: %w", err)
	}
	for _, h := range listed {
		if strings.EqualFold(h.ID, hubID) {
			return ErrHubLive
		}
	}
	return nil
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
// publicAddr, which is also the hub id. The address (a host that can name
// a Redis user, ErrHubIDNotUser) and CSR are checked, and the id refused
// while a live hub uses it (ErrHubLive), before the token is spent, so a typo does not burn it. The signed certificate is
// recorded as trusted (retiring the hub id's older ones), then the hub's
// Redis user is created or reset (a new password; the old one's sessions
// are disconnected), before anything is returned; when either fails the
// token is already spent and the hub needs a new one.
func (u *UseCase) Redeem(ctx context.Context, token, csrPEM, publicAddr string) (Joined, error) {
	if u.certs == nil {
		return Joined{}, errors.New("no hub certificate registry to record the certificate in")
	}
	if u.users == nil {
		return Joined{}, errors.New("no Redis user manager to create the hub's user with")
	}
	hubID, err := hubHost(publicAddr)
	if err != nil {
		return Joined{}, err
	}
	// The hub id names its Redis user: one that cannot have a user (an
	// IPv6 address) is refused before the token is spent.
	if port.CheckHubRedisID(hubID) != nil {
		return Joined{}, ErrHubIDNotUser
	}
	if err := validateCSR(csrPEM); err != nil {
		return Joined{}, err
	}
	if err := u.checkNotLive(ctx, hubID); err != nil {
		return Joined{}, err
	}
	ok, err := u.repo.Consume(ctx, hashToken(token), hubID, u.now())
	if err != nil {
		return Joined{}, fmt.Errorf("redeem hub token: %w", err)
	}
	if !ok {
		return Joined{}, ErrInvalidToken
	}
	certPEM, _, err := u.signer.SignHub(csrPEM, hubID, []string{hubID})
	if err != nil {
		return Joined{}, fmt.Errorf("sign hub certificate: %w", err)
	}
	fp, err := certpkg.Fingerprint(certPEM)
	if err != nil {
		return Joined{}, fmt.Errorf("fingerprint hub certificate: %w", err)
	}
	if err := u.certs.Trust(ctx, hubID, fp, u.now()); err != nil {
		return Joined{}, fmt.Errorf("trust hub certificate (the join token is spent): %w", err)
	}
	user, pass, err := u.users.Ensure(ctx, hubID)
	if err != nil {
		return Joined{}, fmt.Errorf("create the hub's Redis user (the join token is spent): %w", err)
	}
	return Joined{CertPEM: certPEM, CAPEM: u.signer.CACertPEM(), RedisUsername: user, RedisPassword: pass}, nil
}
