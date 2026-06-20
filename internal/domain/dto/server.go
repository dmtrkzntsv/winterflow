package dto

import "time"

type ServerDTO struct {
}

// ClaimServerDTO carries a pairing code and the organization that should own the
// server once the code is claimed.
type ClaimServerDTO struct {
	Code           string
	OrganizationID string
}

type ServerRegistrationDTO struct {
	ServerID             string
	CertificateID        string
	Hostname             string
	Code                 string
	ExpiresAt            time.Time
	Certificate          []byte
	CertificateExpiresAt time.Time
}
