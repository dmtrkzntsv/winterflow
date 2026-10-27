package model

import "time"

// SecretRequestStatus is where a secret request stands.
type SecretRequestStatus string

const (
	// SecretPending waits for the user to submit the values.
	SecretPending SecretRequestStatus = "pending"
	// SecretSubmitted holds the submitted ciphertexts until a save uses them.
	SecretSubmitted SecretRequestStatus = "submitted"
	// SecretUsed: a save was dispatched with the ciphertexts (now deleted).
	SecretUsed SecretRequestStatus = "used"
	// SecretExpired: never submitted, or submitted and never used, in time.
	SecretExpired SecretRequestStatus = "expired"
)

// SecretRequest is a secret link: an agent's request for an app's secret
// values, filled in by a member of OrgID in the browser, which encrypts each
// value to ServerID's key. Only the ciphertexts are ever stored.
type SecretRequest struct {
	ID        string
	OrgID     string
	ServerID  string
	AppID     string // "" when the request is for a new app
	CreatedBy string
	Names     []string
	Notes     map[string]string
	Message   string
	Status    SecretRequestStatus
	// Ciphertexts are the submitted values by name (submitted only).
	Ciphertexts map[string]string
	CreatedAt   time.Time
	// ExpiresAt is the current deadline: of the submission while pending,
	// of the ciphertexts' use while submitted.
	ExpiresAt   time.Time
	SubmittedAt *time.Time
	UsedAt      *time.Time
	EndedAt     *time.Time
}

// StatusAt is the request's status at now: a pending or submitted request
// past its deadline reads expired before the sweep marks it.
func (r SecretRequest) StatusAt(now time.Time) SecretRequestStatus {
	if (r.Status == SecretPending || r.Status == SecretSubmitted) && !now.Before(r.ExpiresAt) {
		return SecretExpired
	}
	return r.Status
}
