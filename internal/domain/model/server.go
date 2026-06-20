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
	Name  string
	Value string
}

type Feature struct {
	Name      string
	IsEnabled bool
}

type Certificate struct {
	Certificate string `json:"certificate"`
}

type Server struct {
	ID             string       `json:"id"`
	OrganizationID string       `json:"organization_id"`
	Name           string       `json:"name"`
	CreatedAt      time.Time    `json:"created_at"`
	LastSeenAt     *time.Time   `json:"last_seen_at"`
	Certificate    Certificate  `json:"certificate"`
	Capabilities   []Capability `json:"capabilities,omitempty"`
	Features       []Feature
}
