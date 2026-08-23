package model

import (
	"errors"
	"time"
)

var (
	// ErrInvalidRegistrationCode is returned when no pending registration
	// matches the supplied code.
	ErrInvalidRegistrationCode = errors.New("invalid registration code")
	// ErrRegistrationCodeExpired is returned when the matching registration
	// has passed its expiry.
	ErrRegistrationCodeExpired = errors.New("registration code has expired")
)

type Capability struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Server struct {
	ID             string       `json:"id"`
	OrganizationID string       `json:"organization_id"`
	Name           string       `json:"name"`
	CreatedAt      time.Time    `json:"created_at"`
	LastSeenAt     *time.Time   `json:"last_seen_at"`
	Capabilities   []Capability `json:"capabilities,omitempty"`
	// Features are agent-advertised booleans (can_install, ingress, ...);
	// the UI gates capability-dependent panels on them.
	Features map[string]bool `json:"features,omitempty"`
}
