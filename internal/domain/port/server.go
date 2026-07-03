package port

import (
	"context"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
)

type ServerRepository interface {
	GetServers(ctx context.Context, userID string) ([]model.Server, error)
	RegisterServer(ctx context.Context, dto dto.ServerRegistrationDTO) error

	// ClaimServer consumes a pending registration identified by its code and
	// materializes a Server (plus its certificate) owned by the given
	// organization. Returns model.ErrInvalidRegistrationCode or
	// model.ErrRegistrationCodeExpired on a bad/stale code.
	ClaimServer(ctx context.Context, dto dto.ClaimServerDTO) (model.Server, error)

	// PendingRegistrationCode returns the code of the most recent unclaimed
	// registration. Used by standalone auto-claim, where there is one embedded
	// agent and no code to type.
	PendingRegistrationCode(ctx context.Context) (string, bool, error)

	// HasAnyServer reports whether any server has been claimed.
	HasAnyServer(ctx context.Context) (bool, error)

	// ClearPendingRegistrations removes all unclaimed registrations.
	ClearPendingRegistrations(ctx context.Context) error

	// TouchLastSeen records that a server reported in (durable info).
	TouchLastSeen(ctx context.Context, serverID string) error

	// SaveCapabilities upserts a server's capabilities and features.
	SaveCapabilities(ctx context.Context, serverID string, capabilities map[string]string, features map[string]bool) error

	// GetServerUserIDs returns the user ids of every member of the org that
	// owns the server (recipients of unsolicited status notifications).
	GetServerUserIDs(ctx context.Context, serverID string) ([]string, error)

	// GetCapability returns a single capability value for a server. ok is false
	// if the server has no such capability recorded.
	GetCapability(ctx context.Context, serverID, name string) (value string, ok bool, err error)
}

type ServerService interface {
	GetServers(ctx context.Context, userID string) ([]model.Server, error)
	ClaimServer(ctx context.Context, dto dto.ClaimServerDTO) (model.Server, error)
	PendingRegistrationCode(ctx context.Context) (string, bool, error)
}
