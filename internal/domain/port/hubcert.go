package port

import (
	"context"
	"errors"
	"time"
)

// ErrHubCertRetired: Trust was given a certificate that was retired before.
// A retired certificate is never trusted again (it may have leaked); issue
// a new one.
var ErrHubCertRetired = errors.New("this hub certificate was retired; issue a new one")

// ErrHubCertOtherHub: Trust was given a fingerprint already recorded for
// another hub id.
var ErrHubCertOtherHub = errors.New("this hub certificate is recorded for another hub id")

// HubCertRepository records the hub server certificates the API trusts.
// Agents pin the active (not retired) set. A fingerprint is the lowercase
// hex SHA-256 of the certificate's DER.
type HubCertRepository interface {
	// Trust records fingerprint for hubID (a no-op when it is already
	// active) and retires every other active certificate of hubID, in one
	// transaction. A retired fingerprint is refused (ErrHubCertRetired), as
	// is one recorded for another hub id (ErrHubCertOtherHub).
	Trust(ctx context.Context, hubID, fingerprint string, now time.Time) error
	// Retire retires the active certificates of hubID, or the one with
	// fingerprint (exactly one of the two is non-empty), and returns how
	// many it retired and the hub ids it concerns that have no active
	// certificate left (by hub id: hubID itself; by fingerprint: the
	// certificate's hub id, when it was its last active one). Both are
	// reported even when nothing was retired this time, so a rerun can
	// finish cleaning up after a hub whose certificates were retired before.
	Retire(ctx context.Context, hubID, fingerprint string, now time.Time) (retired int, orphaned []string, err error)
	// Active lists the active fingerprints, sorted.
	Active(ctx context.Context) ([]string, error)
}
