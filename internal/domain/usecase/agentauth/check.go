// Package agentauth answers the hubs' agent authentication checks and
// publishes revocations. The hub terminates mTLS against the CA; the API
// decides whether the presented certificate is still its server's active one.
package agentauth

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/cert"
)

// store is the slice of the server repository the check reads.
type store interface {
	ServerHub(ctx context.Context, serverID string) (string, error)
	ActiveServerCertificate(ctx context.Context, serverID string) (string, error)
}

var _ store = port.ServerRepository(nil)

// Check decides whether the certificate with the given fingerprint may
// connect as serverID to the hub hubID: no such server is "unknown", a
// fingerprint other than the server's active certificate is "revoked", a
// server assigned to another hub (or to none) is "moved" (the agent asks the
// API where to go), else "allowed". A repository error is returned: the
// caller sends no reply and the hub fails the stream as unavailable instead
// of denying a good agent.
func Check(ctx context.Context, repo store, serverID, fingerprint, hubID string) (bus.AgentVerdict, string, error) {
	assigned, err := repo.ServerHub(ctx, serverID)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return bus.VerdictUnknown, "server not found", nil
		}
		return "", "", err
	}
	stored, err := repo.ActiveServerCertificate(ctx, serverID)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return bus.VerdictRevoked, "certificate is not active", nil
		}
		return "", "", err
	}
	if fingerprint == "" || activeFingerprint(stored) != fingerprint {
		return bus.VerdictRevoked, "certificate is not active", nil
	}
	if assigned != hubID {
		return bus.VerdictMoved, "server belongs to another hub", nil
	}
	return bus.VerdictAllowed, "", nil
}

// CertPEM returns a stored certificate as PEM: stored as is when it reads
// as one, else the base64-decoded PEM the old registration flow stored. ""
// when neither reads.
func CertPEM(stored string) string {
	if _, err := cert.Fingerprint(stored); err == nil {
		return stored
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stored))
	if err != nil {
		return ""
	}
	if _, err := cert.Fingerprint(string(raw)); err != nil {
		return ""
	}
	return string(raw)
}

// activeFingerprint fingerprints a stored certificate (see CertPEM). ""
// when unreadable.
func activeFingerprint(stored string) string {
	p := CertPEM(stored)
	if p == "" {
		return ""
	}
	fp, err := cert.Fingerprint(p)
	if err != nil {
		return ""
	}
	return fp
}
