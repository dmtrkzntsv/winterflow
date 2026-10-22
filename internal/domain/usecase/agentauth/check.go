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
// connect as serverID: no such server is "unknown", a fingerprint other than
// the server's active certificate is "revoked", else "allowed". A repository
// error is returned: the caller sends no reply and the hub fails the stream
// as unavailable instead of denying a good agent.
func Check(ctx context.Context, repo store, serverID, fingerprint string) (bus.AgentVerdict, string, error) {
	if _, err := repo.ServerHub(ctx, serverID); err != nil {
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
	if fingerprint != "" && activeFingerprint(stored) == fingerprint {
		return bus.VerdictAllowed, "", nil
	}
	return bus.VerdictRevoked, "certificate is not active", nil
}

// activeFingerprint fingerprints a stored certificate: a PEM, or the
// base64-encoded PEM the old registration flow stored. "" when unreadable.
func activeFingerprint(stored string) string {
	if fp, err := cert.Fingerprint(stored); err == nil {
		return fp
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stored))
	if err != nil {
		return ""
	}
	fp, err := cert.Fingerprint(string(raw))
	if err != nil {
		return ""
	}
	return fp
}
