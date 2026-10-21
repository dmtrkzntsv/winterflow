// Package enrollment implements port.EnrollmentRepository: agent
// enrollments and the claim that turns one into a server.
package enrollment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
)

type (
	row           = sqlitegen.AgentEnrollment
	insertParams  = sqlitegen.InsertEnrollmentParams
	replaceParams = sqlitegen.ReplaceEnrollmentCodeParams
	claimParams   = sqlitegen.ClaimEnrollmentParams
	pruneParams   = sqlitegen.PruneEnrollmentsParams
	serverParams  = sqlitegen.InsertServerWithHubParams
	certParams    = sqlitegen.InsertCertificateParams
)

type querier interface {
	InsertEnrollment(ctx context.Context, db backend.DBTX, p insertParams) error
	GetEnrollmentBySecretHash(ctx context.Context, db backend.DBTX, hash string) (row, error)
	GetEnrollmentByCode(ctx context.Context, db backend.DBTX, code string) (row, error)
	GetEnrollmentByID(ctx context.Context, db backend.DBTX, id string) (row, error)
	ReplaceEnrollmentCode(ctx context.Context, db backend.DBTX, p replaceParams) (int64, error)
	MarkEnrollmentFailed(ctx context.Context, db backend.DBTX, id string) error
	ClaimEnrollment(ctx context.Context, db backend.DBTX, p claimParams) (int64, error)
	InsertServerWithHub(ctx context.Context, db backend.DBTX, p serverParams) error
	InsertCertificate(ctx context.Context, db backend.DBTX, p certParams) error
	CountPendingEnrollments(ctx context.Context, db backend.DBTX) (int64, error)
	CountPendingEnrollmentsByIP(ctx context.Context, db backend.DBTX, ip string) (int64, error)
	PruneEnrollments(ctx context.Context, db backend.DBTX, p pruneParams) error
}

const (
	pendingMaxAge = 2 * time.Hour
	// Past the usecase's 15-minute ClaimedTTL a claimed row only answers
	// "uncollected"; it is kept that long so a late agent learns why.
	claimedMaxAge = 2 * time.Hour
	maxNameLen    = 64
)

// Repo is the enrollments repository.
type Repo struct {
	tx *backend.TxManager
	q  querier
}

var _ port.EnrollmentRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}}, nil
	}
	return nil, fmt.Errorf("enrollment repository: unknown database driver %q", driver)
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func optTime(t time.Time) *types.DateTime {
	if t.IsZero() {
		return nil
	}
	dt := types.DateTime(t)
	return &dt
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefTime(t *types.DateTime) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.Time()
}

func hydrate(r row) model.Enrollment {
	return model.Enrollment{
		ID:             r.EnrollmentID,
		PollSecretHash: r.PollSecretHash,
		CSR:            r.Csr,
		Hostname:       r.Hostname,
		Code:           deref(r.Code),
		CodeExpiresAt:  derefTime(r.CodeExpiresAt),
		CodesIssued:    int(r.CodesIssued),
		State:          model.EnrollmentState(r.State),
		ServerID:       deref(r.ServerID),
		Certificate:    deref(r.Certificate),
		HubAddr:        deref(r.HubAddr),
		ClientIP:       r.ClientIp,
		ClaimedAt:      derefTime(r.ClaimedAt),
		CreatedAt:      r.CreatedAt.Time(),
	}
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return model.ErrNotFound
	}
	return err
}

// Create stores a new enrollment.
func (r *Repo) Create(ctx context.Context, e model.Enrollment) error {
	created := e.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	return r.q.InsertEnrollment(ctx, r.tx.Querier(ctx), insertParams{
		EnrollmentID:   e.ID,
		PollSecretHash: e.PollSecretHash,
		Csr:            e.CSR,
		Hostname:       e.Hostname,
		Code:           optString(e.Code),
		CodeExpiresAt:  optTime(e.CodeExpiresAt),
		CodesIssued:    int64(e.CodesIssued),
		State:          string(e.State),
		CreatedAt:      types.DateTime(created),
		ClientIp:       e.ClientIP,
	})
}

// GetBySecretHash returns the enrollment polled with that secret hash.
func (r *Repo) GetBySecretHash(ctx context.Context, hash string) (model.Enrollment, error) {
	row, err := r.q.GetEnrollmentBySecretHash(ctx, r.tx.Querier(ctx), hash)
	if err != nil {
		return model.Enrollment{}, notFound(err)
	}
	return hydrate(row), nil
}

// GetByCode returns the pending enrollment holding the code.
func (r *Repo) GetByCode(ctx context.Context, code string) (model.Enrollment, error) {
	row, err := r.q.GetEnrollmentByCode(ctx, r.tx.Querier(ctx), code)
	if err != nil {
		return model.Enrollment{}, notFound(err)
	}
	return hydrate(row), nil
}

// ReplaceCode swaps in a fresh code for a pending enrollment.
func (r *Repo) ReplaceCode(ctx context.Context, id, code string, expiresAt time.Time, codesIssued int) error {
	n, err := r.q.ReplaceEnrollmentCode(ctx, r.tx.Querier(ctx), replaceParams{
		Code:          &code,
		CodeExpiresAt: optTime(expiresAt),
		CodesIssued:   int64(codesIssued),
		EnrollmentID:  id,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return model.ErrNotFound
	}
	return nil
}

// MarkFailed ends a pending enrollment and frees its code.
func (r *Repo) MarkFailed(ctx context.Context, id string) error {
	return r.q.MarkEnrollmentFailed(ctx, r.tx.Querier(ctx), id)
}

// Claim consumes the code, creating the server (named after the agent's
// hostname) and its active certificate in one transaction.
func (r *Repo) Claim(ctx context.Context, c port.ClaimEnrollment) (model.Server, error) {
	var srv model.Server
	err := r.tx.WithTx(ctx, func(ctx context.Context) error {
		db := r.tx.Querier(ctx)
		now := types.DateTime(c.Now)
		n, err := r.q.ClaimEnrollment(ctx, db, claimParams{
			ServerID:      &c.ServerID,
			Certificate:   &c.Certificate,
			HubAddr:       &c.HubAddr,
			ClaimedAt:     &now,
			EnrollmentID:  c.EnrollmentID,
			Code:          &c.Code,
			CodeExpiresAt: &now,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return model.ErrRegistrationCodeExpired
		}
		e, err := r.q.GetEnrollmentByID(ctx, db, c.EnrollmentID)
		if err != nil {
			return err
		}
		name := truncate(e.Hostname, maxNameLen)
		created := types.DateTime(c.Now)
		if err := r.q.InsertServerWithHub(ctx, db, serverParams{
			ServerID:       c.ServerID,
			OrganizationID: c.OrganizationID,
			Name:           name,
			CreatedAt:      created,
			HubID:          optString(c.HubID),
		}); err != nil {
			return err
		}
		if err := r.q.InsertCertificate(ctx, db, certParams{
			CertificateID: c.CertificateID,
			ServerID:      c.ServerID,
			Certificate:   c.Certificate,
			IsActive:      true,
			ExpiresAt:     types.DateTime(c.CertExpiresAt),
			CreatedAt:     created,
		}); err != nil {
			return err
		}
		srv = model.Server{ID: c.ServerID, OrganizationID: c.OrganizationID, Name: name, CreatedAt: created.Time()}
		return nil
	})
	if err != nil {
		return model.Server{}, err
	}
	return srv, nil
}

func truncate(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}

// CountPending returns how many enrollments are waiting for a claim.
func (r *Repo) CountPending(ctx context.Context) (int, error) {
	n, err := r.q.CountPendingEnrollments(ctx, r.tx.Querier(ctx))
	return int(n), err
}

// CountPendingByIP returns how many pending enrollments ip started.
func (r *Repo) CountPendingByIP(ctx context.Context, ip string) (int, error) {
	n, err := r.q.CountPendingEnrollmentsByIP(ctx, r.tx.Querier(ctx), ip)
	return int(n), err
}

// Prune deletes stale enrollments.
func (r *Repo) Prune(ctx context.Context, now time.Time) error {
	claimedBefore := types.DateTime(now.Add(-claimedMaxAge))
	return r.q.PruneEnrollments(ctx, r.tx.Querier(ctx), pruneParams{
		CreatedAt: types.DateTime(now.Add(-pendingMaxAge)),
		ClaimedAt: &claimedBefore,
	})
}
