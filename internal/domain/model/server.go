package model

import "time"

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
