package port

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
)

// ClaimEnrollment is what a successful claim materializes: the server (in
// OrganizationID, assigned to HubID) and its active certificate.
type ClaimEnrollment struct {
	EnrollmentID, Code, ServerID, OrganizationID, HubID, CertificateID, Certificate string
	CertExpiresAt, Now                                                              time.Time
}

// EnrollmentRepository stores agent enrollments (distributed topology).
type EnrollmentRepository interface {
	Create(ctx context.Context, e model.Enrollment) error
	// GetBySecretHash returns model.ErrNotFound when absent.
	GetBySecretHash(ctx context.Context, hash string) (model.Enrollment, error)
	// GetByCode returns model.ErrNotFound when absent.
	GetByCode(ctx context.Context, code string) (model.Enrollment, error)
	// ReplaceCode swaps in a new code; only a pending enrollment changes
	// (model.ErrNotFound otherwise).
	ReplaceCode(ctx context.Context, id, code string, expiresAt time.Time, codesIssued int) error
	MarkFailed(ctx context.Context, id string) error
	// Claim consumes a pending, unexpired code in one transaction, creating
	// the server and its active certificate. model.ErrRegistrationCodeExpired
	// when the code no longer matches a pending enrollment or has expired.
	Claim(ctx context.Context, c ClaimEnrollment) (model.Server, error)
	CountPending(ctx context.Context) (int, error)
	// Prune deletes pending/failed rows older than 2 h and claimed rows
	// claimed more than 15 min ago.
	Prune(ctx context.Context, now time.Time) error
}
