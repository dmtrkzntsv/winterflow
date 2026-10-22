// Package hubpins reads the trusted hub certificate set agents pin, for the
// answers that carry it (enrollment status, agent hub).
package hubpins

import (
	"context"
	"errors"
	"fmt"

	"winterflow/internal/domain/port"
)

// ErrUnavailable: the set could not be read. No answer goes out without
// it (503; the agent asks again).
var ErrUnavailable = errors.New("trusted hub certificates unavailable")

// Source is the slice of port.HubCertRepository the readers use.
type Source interface {
	Active(ctx context.Context) ([]string, error)
}

var _ Source = port.HubCertRepository(nil)

// Active reads the set from src: fingerprints (lowercase hex SHA-256 of
// each DER), never nil on success. A nil src or a read error is
// ErrUnavailable.
func Active(ctx context.Context, src Source) ([]string, error) {
	if src == nil {
		return nil, fmt.Errorf("%w: no hub certificate registry", ErrUnavailable)
	}
	fps, err := src.Active(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if fps == nil {
		fps = []string{}
	}
	return fps, nil
}
