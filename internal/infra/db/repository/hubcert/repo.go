// Package hubcert implements port.HubCertRepository: the hub server
// certificates the API trusts, by fingerprint.
package hubcert

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
)

type (
	insertParams      = sqlitegen.InsertHubCertificateParams
	getRow            = sqlitegen.GetHubCertificateRow
	retireOtherParams = sqlitegen.RetireOtherHubCertificatesParams
	retireHubParams   = sqlitegen.RetireHubCertificatesByHubParams
	retireFpParams    = sqlitegen.RetireHubCertificateByFingerprintParams
)

type querier interface {
	LockHubCertificates(ctx context.Context, db backend.DBTX, hubID string) error
	InsertHubCertificate(ctx context.Context, db backend.DBTX, p insertParams) error
	GetHubCertificate(ctx context.Context, db backend.DBTX, fingerprint string) (getRow, error)
	RetireOtherHubCertificates(ctx context.Context, db backend.DBTX, p retireOtherParams) error
	RetireHubCertificatesByHub(ctx context.Context, db backend.DBTX, p retireHubParams) (int64, error)
	RetireHubCertificateByFingerprint(ctx context.Context, db backend.DBTX, p retireFpParams) (int64, error)
	ListActiveHubCertificates(ctx context.Context, db backend.DBTX) ([]string, error)
	CountActiveHubCertificatesByHub(ctx context.Context, db backend.DBTX, hubID string) (int64, error)
	CountHubCertificatesByHub(ctx context.Context, db backend.DBTX, hubID string) (int64, error)
}

// Repo is the hub certificates repository.
type Repo struct {
	tx *backend.TxManager
	q  querier
}

var _ port.HubCertRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}}, nil
	}
	return nil, fmt.Errorf("hub certificate repository: unknown database driver %q", driver)
}

// Trust inserts the fingerprint (idempotent) and retires the hub's other
// active certificates in one transaction. On Postgres the transaction
// first takes a per-hub-id advisory lock, so two concurrent Trusts of one
// hub id run one after the other and only the later certificate stays
// active (under READ COMMITTED each would otherwise miss the other's
// uncommitted row); SQLite serializes transactions already.
func (r *Repo) Trust(ctx context.Context, hubID, fingerprint string, now time.Time) error {
	if hubID == "" || fingerprint == "" {
		return errors.New("hub id and fingerprint are required")
	}
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		db := r.tx.Querier(ctx)
		if err := r.q.LockHubCertificates(ctx, db, hubID); err != nil {
			return fmt.Errorf("lock hub certificates: %w", err)
		}
		if err := r.q.InsertHubCertificate(ctx, db, insertParams{
			Fingerprint: fingerprint, HubID: hubID, CreatedAt: types.DateTime(now),
		}); err != nil {
			return fmt.Errorf("insert hub certificate: %w", err)
		}
		row, err := r.q.GetHubCertificate(ctx, db, fingerprint)
		if err != nil {
			return fmt.Errorf("read hub certificate: %w", err)
		}
		if row.HubID != hubID {
			return port.ErrHubCertOtherHub
		}
		if row.RetiredAt != nil {
			return port.ErrHubCertRetired
		}
		at := types.DateTime(now)
		if err := r.q.RetireOtherHubCertificates(ctx, db, retireOtherParams{
			RetiredAt: &at, HubID: hubID, Fingerprint: fingerprint,
		}); err != nil {
			return fmt.Errorf("retire older hub certificates: %w", err)
		}
		return nil
	})
}

// Retire retires by hub id or by fingerprint (exactly one non-empty) in
// one transaction, then releases the hub if it has no active certificate
// left, in a second one under the per-hub lock (see port.HubCertRepository).
func (r *Repo) Retire(ctx context.Context, hubID, fingerprint string, now time.Time, release func(ctx context.Context, hubID string) error) (int, error) {
	if (hubID == "") == (fingerprint == "") {
		return 0, errors.New("give exactly one of hub id and fingerprint")
	}
	at := types.DateTime(now)
	var (
		n      int64
		target string // the hub to release when it has no active certificate
	)
	err := r.tx.WithTx(ctx, func(ctx context.Context) error {
		db := r.tx.Querier(ctx)
		target = hubID
		if fingerprint != "" {
			row, err := r.q.GetHubCertificate(ctx, db, fingerprint)
			if errors.Is(err, sql.ErrNoRows) {
				target = ""
				return nil // an unknown fingerprint: nothing to retire
			}
			if err != nil {
				return fmt.Errorf("read hub certificate: %w", err)
			}
			target = row.HubID
		}
		if err := r.q.LockHubCertificates(ctx, db, target); err != nil {
			return fmt.Errorf("lock hub certificates: %w", err)
		}
		var err error
		if fingerprint != "" {
			n, err = r.q.RetireHubCertificateByFingerprint(ctx, db, retireFpParams{RetiredAt: &at, Fingerprint: fingerprint})
			return err
		}
		if n, err = r.q.RetireHubCertificatesByHub(ctx, db, retireHubParams{RetiredAt: &at, HubID: hubID}); err != nil {
			return err
		}
		if n == 0 {
			// A hub id with no certificate on record (a typo) is not released.
			total, err := r.q.CountHubCertificatesByHub(ctx, db, hubID)
			if err != nil {
				return fmt.Errorf("count hub certificates: %w", err)
			}
			if total == 0 {
				target = ""
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if release == nil || target == "" {
		return int(n), nil
	}
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		db := r.tx.Querier(ctx)
		if err := r.q.LockHubCertificates(ctx, db, target); err != nil {
			return fmt.Errorf("lock hub certificates: %w", err)
		}
		left, err := r.q.CountActiveHubCertificatesByHub(ctx, db, target)
		if err != nil {
			return fmt.Errorf("count active hub certificates: %w", err)
		}
		if left > 0 {
			return nil
		}
		return release(ctx, target)
	})
	if err != nil {
		return int(n), fmt.Errorf("release hub %s: %w", target, err)
	}
	return int(n), nil
}

// Active lists the active fingerprints, sorted; never nil.
func (r *Repo) Active(ctx context.Context) ([]string, error) {
	fps, err := r.q.ListActiveHubCertificates(ctx, r.tx.Querier(ctx))
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if fps == nil {
		fps = []string{}
	}
	return fps, nil
}
