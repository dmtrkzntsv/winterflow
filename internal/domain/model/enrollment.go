package model

import "time"

// EnrollmentState is where an agent enrollment stands.
type EnrollmentState string

const (
	EnrollmentPending EnrollmentState = "pending"
	EnrollmentClaimed EnrollmentState = "claimed"
	EnrollmentFailed  EnrollmentState = "failed"
)

// Enrollment is an agent's code-based pairing request: its CSR, the
// short-lived code a user claims in the UI, and (once claimed) the issued
// certificate. Zero Code/CodeExpiresAt/ServerID/Certificate/ClaimedAt mean
// "not set".
type Enrollment struct {
	ID             string
	PollSecretHash string
	CSR            string
	Hostname       string
	Code           string
	CodeExpiresAt  time.Time
	CodesIssued    int
	State          EnrollmentState
	ServerID       string
	Certificate    string
	HubAddr        string // public address of the hub assigned at the claim
	ClientIP       string // address that started the enrollment
	ClaimedAt      time.Time
	CreatedAt      time.Time
}
