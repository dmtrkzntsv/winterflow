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
	// many it retired. Then, for the hub id concerned (one with any
	// certificate on record; by fingerprint, the certificate's hub), it
	// calls release (when non-nil) if the hub has no active certificate
	// left, while holding the per-hub lock Trust takes, so a hub that
	// joins at the same moment is not released after its new certificate
	// was trusted. This happens even when nothing was retired this time,
	// so a rerun finishes a release that failed. A release error is
	// returned (wrapped) after the retire was committed.
	Retire(ctx context.Context, hubID, fingerprint string, now time.Time, release func(ctx context.Context, hubID string) error) (int, error)
	// Active lists the active fingerprints, sorted.
	Active(ctx context.Context) ([]string, error)
}
