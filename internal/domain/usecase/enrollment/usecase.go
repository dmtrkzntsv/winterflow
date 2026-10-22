// Package enrollment pairs an agent with an organization by a short-lived
// code: the agent submits a CSR and polls with a secret; a user claims the
// code in the UI, which creates the server and signs the agent's certificate.
package enrollment

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
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/util"
)

const (
	CodeTTL    = 10 * time.Minute
	MaxCodes   = 10
	ClaimedTTL = 15 * time.Minute
	MaxPending = 1000
	// MaxPendingPerIP caps the pending enrollments one client address may
	// hold, so a single client cannot fill MaxPending.
	MaxPendingPerIP = 10

	codeLength  = 6
	codeRetries = 5
)

var (
	ErrTooManyPending       = errors.New("too many pending enrollments, try again later")
	ErrTooManyPendingFromIP = errors.New("too many pending enrollments from this address, try again later")
	ErrInvalidCSR           = errors.New("invalid certificate request")
	ErrNoCodesLeft          = errors.New("no registration codes left, restart the installer")
	// ErrPinsUnavailable: a claimed enrollment's answer needs the trusted
	// hub certificate set, which could not be read (503; the agent polls
	// again).
	ErrPinsUnavailable = errors.New("trusted hub certificates unavailable")
)

// Pins is the slice of port.HubCertRepository the usecase reads: the
// fingerprints of the hub certificates the API trusts.
type Pins interface {
	Active(ctx context.Context) ([]string, error)
}

var _ Pins = port.HubCertRepository(nil)

// Signer issues agent certificates from the control plane's CA.
type Signer interface {
	Sign(csrPEM, commonName string) (certPEM string, expiresAt time.Time, err error)
	CACertPEM() string
}

// HubPicker chooses the hub a new server is assigned to (*hubs.Picker).
type HubPicker interface {
	Pick(ctx context.Context) (model.Hub, error)
}

type StartResult struct {
	EnrollmentID, PollSecret, Code string
	ExpiresAt                      time.Time
	CodesLeft                      int
}

type Status struct {
	State                                         string // pending|expired|failed|claimed|uncollected
	Code                                          string
	ExpiresAt                                     time.Time
	CodesLeft                                     int
	ServerID, Certificate, CACertificate, HubAddr string
	// HubFingerprints is the trusted hub certificate set (claimed only;
	// lowercase hex SHA-256 of each DER), which the agent pins.
	HubFingerprints []string
}

type UseCase struct {
	repo   port.EnrollmentRepository
	signer Signer
	hubs   HubPicker
	pins   Pins
	now    func() time.Time
}

func New(repo port.EnrollmentRepository, signer Signer, hubs HubPicker, pins Pins, now func() time.Time) *UseCase {
	if now == nil {
		now = time.Now
	}
	return &UseCase{repo: repo, signer: signer, hubs: hubs, pins: pins, now: now}
}

func hashSecret(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
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
		return fmt.Errorf("%w: signature: %v", ErrInvalidCSR, err)
	}
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return fmt.Errorf("%w: key must be ECDSA P-256", ErrInvalidCSR)
	}
	return nil
}

// Start stores a new enrollment started from clientIP and returns its first
// code and poll secret.
func (u *UseCase) Start(ctx context.Context, csrPEM, hostname, clientIP string) (StartResult, error) {
	if err := validateCSR(csrPEM); err != nil {
		return StartResult{}, err
	}
	now := u.now()
	if err := u.repo.Prune(ctx, now); err != nil {
		return StartResult{}, fmt.Errorf("prune enrollments: %w", err)
	}
	n, err := u.repo.CountPending(ctx)
	if err != nil {
		return StartResult{}, fmt.Errorf("count enrollments: %w", err)
	}
	if n >= MaxPending {
		return StartResult{}, ErrTooManyPending
	}
	if n, err := u.repo.CountPendingByIP(ctx, clientIP); err != nil {
		return StartResult{}, fmt.Errorf("count enrollments: %w", err)
	} else if n >= MaxPendingPerIP {
		return StartResult{}, ErrTooManyPendingFromIP
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return StartResult{}, fmt.Errorf("poll secret: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	e := model.Enrollment{
		ID:             util.GenerateID(),
		PollSecretHash: hashSecret(secret),
		CSR:            csrPEM,
		Hostname:       hostname,
		CodeExpiresAt:  now.Add(CodeTTL),
		CodesIssued:    1,
		State:          model.EnrollmentPending,
		ClientIP:       clientIP,
		CreatedAt:      now,
	}
	for i := 0; i < codeRetries; i++ {
		e.Code = util.GenerateRandomCode(codeLength)
		if err = u.repo.Create(ctx, e); err == nil {
			return StartResult{EnrollmentID: e.ID, PollSecret: secret, Code: e.Code, ExpiresAt: e.CodeExpiresAt, CodesLeft: MaxCodes - 1}, nil
		}
	}
	return StartResult{}, fmt.Errorf("create enrollment: %w", err)
}

// Refresh replaces the code of a pending enrollment; the MaxCodes-th code
// is the last one.
func (u *UseCase) Refresh(ctx context.Context, pollSecret string) (StartResult, error) {
	e, err := u.repo.GetBySecretHash(ctx, hashSecret(pollSecret))
	if err != nil {
		return StartResult{}, err
	}
	if e.State != model.EnrollmentPending {
		return StartResult{}, model.ErrNotFound
	}
	if e.CodesIssued >= MaxCodes {
		if err := u.repo.MarkFailed(ctx, e.ID); err != nil {
			return StartResult{}, err
		}
		return StartResult{}, ErrNoCodesLeft
	}
	expires := u.now().Add(CodeTTL)
	issued := e.CodesIssued + 1
	for i := 0; i < codeRetries; i++ {
		code := util.GenerateRandomCode(codeLength)
		if err = u.repo.ReplaceCode(ctx, e.ID, code, expires, issued); err == nil {
			return StartResult{EnrollmentID: e.ID, PollSecret: pollSecret, Code: code, ExpiresAt: expires, CodesLeft: MaxCodes - issued}, nil
		}
		if errors.Is(err, model.ErrNotFound) {
			return StartResult{}, err
		}
	}
	return StartResult{}, fmt.Errorf("replace code: %w", err)
}

// Status reports where the enrollment polled with pollSecret stands.
func (u *UseCase) Status(ctx context.Context, pollSecret string) (Status, error) {
	e, err := u.repo.GetBySecretHash(ctx, hashSecret(pollSecret))
	if err != nil {
		return Status{}, err
	}
	now := u.now()
	left := MaxCodes - e.CodesIssued
	switch e.State {
	case model.EnrollmentClaimed:
		if !e.ClaimedAt.IsZero() && now.After(e.ClaimedAt.Add(ClaimedTTL)) {
			// The server exists but its certificate is no longer handed out.
			return Status{State: "uncollected"}, nil
		}
		fps, err := u.activePins(ctx)
		if err != nil {
			return Status{}, err
		}
		return Status{State: "claimed", CodesLeft: left, ServerID: e.ServerID, Certificate: e.Certificate,
			CACertificate: u.signer.CACertPEM(), HubAddr: e.HubAddr, HubFingerprints: fps}, nil
	case model.EnrollmentFailed:
		return Status{State: "failed"}, nil
	}
	if e.Code == "" || !now.Before(e.CodeExpiresAt) {
		if e.CodesIssued >= MaxCodes {
			return Status{State: "failed"}, nil
		}
		return Status{State: "expired", CodesLeft: left}, nil
	}
	return Status{State: "pending", Code: e.Code, ExpiresAt: e.CodeExpiresAt, CodesLeft: left}, nil
}

// activePins reads the trusted hub certificate set; never nil on success.
func (u *UseCase) activePins(ctx context.Context) ([]string, error) {
	if u.pins == nil {
		return nil, fmt.Errorf("%w: no hub certificate registry", ErrPinsUnavailable)
	}
	fps, err := u.pins.Active(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPinsUnavailable, err)
	}
	if fps == nil {
		fps = []string{}
	}
	return fps, nil
}

// Claim binds the enrollment holding code to orgID: picks a hub, creates the
// server and signs its certificate with CN = server id.
func (u *UseCase) Claim(ctx context.Context, code, orgID string) (model.Server, error) {
	e, err := u.repo.GetByCode(ctx, code)
	if errors.Is(err, model.ErrNotFound) {
		return model.Server{}, model.ErrInvalidRegistrationCode
	}
	if err != nil {
		return model.Server{}, err
	}
	now := u.now()
	if e.State != model.EnrollmentPending || !now.Before(e.CodeExpiresAt) {
		return model.Server{}, model.ErrRegistrationCodeExpired
	}
	hub, err := u.hubs.Pick(ctx)
	if err != nil {
		return model.Server{}, err
	}
	serverID := util.GenerateID()
	certPEM, expires, err := u.signer.Sign(e.CSR, serverID)
	if err != nil {
		return model.Server{}, fmt.Errorf("sign certificate: %w", err)
	}
	srv, err := u.repo.Claim(ctx, port.ClaimEnrollment{
		EnrollmentID:   e.ID,
		Code:           code,
		ServerID:       serverID,
		OrganizationID: orgID,
		HubID:          hub.ID,
		HubAddr:        hub.PublicAddr,
		CertificateID:  util.GenerateID(),
		Certificate:    certPEM,
		CertExpiresAt:  expires,
		Now:            now,
	})
	if err != nil {
		return model.Server{}, err
	}
	return srv, nil
}
