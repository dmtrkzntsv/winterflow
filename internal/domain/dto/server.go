package dto

import "time"

type ServerDTO struct {
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
