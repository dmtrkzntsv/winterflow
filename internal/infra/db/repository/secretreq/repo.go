// Package secretreq implements port.SecretRequestRepository: secret links.
package secretreq

import (
	"context"
	"database/sql"
	"encoding/json"
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
	row           = sqlitegen.SecretRequest
	insertParams  = sqlitegen.InsertSecretRequestParams
	getParams     = sqlitegen.GetSecretRequestParams
	submitParams  = sqlitegen.SubmitSecretRequestParams
	useParams     = sqlitegen.UseSecretRequestParams
	claimParams   = sqlitegen.ClaimSecretRequestParams
	holdParams    = sqlitegen.HoldSecretRequestParams
	releaseParams = sqlitegen.ReleaseSecretRequestParams
)

type querier interface {
	InsertSecretRequest(ctx context.Context, db backend.DBTX, p insertParams) error
	GetSecretRequestByTokenHash(ctx context.Context, db backend.DBTX, tokenHash string) (row, error)
	GetSecretRequest(ctx context.Context, db backend.DBTX, p getParams) (row, error)
	SubmitSecretRequest(ctx context.Context, db backend.DBTX, p submitParams) (int64, error)
	UseSecretRequest(ctx context.Context, db backend.DBTX, p useParams) (int64, error)
	ClaimSecretRequest(ctx context.Context, db backend.DBTX, p claimParams) (int64, error)
	HoldSecretRequest(ctx context.Context, db backend.DBTX, p holdParams) (int64, error)
	ReleaseSecretRequest(ctx context.Context, db backend.DBTX, p releaseParams) (int64, error)
	ExpireSecretRequests(ctx context.Context, db backend.DBTX, now *types.DateTime) (int64, error)
	PruneSecretRequests(ctx context.Context, db backend.DBTX, cutoff *types.DateTime) (int64, error)
}

// Repo is the secret requests repository.
type Repo struct {
	tx *backend.TxManager
	q  querier
}

var _ port.SecretRequestRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}}, nil
	}
	return nil, fmt.Errorf("secret request repository: unknown database driver %q", driver)
}

// Create inserts r as pending.
func (r *Repo) Create(ctx context.Context, req model.SecretRequest, tokenHash string) error {
	names, err := json.Marshal(req.Names)
	if err != nil {
		return err
	}
	notes := req.Notes
	if notes == nil {
		notes = map[string]string{}
	}
	notesJSON, err := json.Marshal(notes)
	if err != nil {
		return err
	}
	var appID *string
	if req.AppID != "" {
		appID = &req.AppID
	}
	return r.q.InsertSecretRequest(ctx, r.tx.Querier(ctx), insertParams{
		ID:        req.ID,
		OrgID:     req.OrgID,
		ServerID:  req.ServerID,
		AppID:     appID,
		CreatedBy: req.CreatedBy,
		Names:     string(names),
		Notes:     string(notesJSON),
		Message:   req.Message,
		TokenHash: tokenHash,
		CreatedAt: types.DateTime(req.CreatedAt),
		ExpiresAt: types.DateTime(req.ExpiresAt),
	})
}

// GetByTokenHash reads the request stored under tokenHash.
func (r *Repo) GetByTokenHash(ctx context.Context, tokenHash string) (model.SecretRequest, error) {
	return hydrate(r.q.GetSecretRequestByTokenHash(ctx, r.tx.Querier(ctx), tokenHash))
}

// Get reads the org's request.
func (r *Repo) Get(ctx context.Context, orgID, id string) (model.SecretRequest, error) {
	return hydrate(r.q.GetSecretRequest(ctx, r.tx.Querier(ctx), getParams{ID: id, OrgID: orgID}))
}

// Submit stores the ciphertexts of a pending, unexpired request.
func (r *Repo) Submit(ctx context.Context, id string, ciphertexts map[string]string, now, expiresAt time.Time) (bool, error) {
	raw, err := json.Marshal(ciphertexts)
	if err != nil {
		return false, err
	}
	s, at := string(raw), types.DateTime(now)
	n, err := r.q.SubmitSecretRequest(ctx, r.tx.Querier(ctx), submitParams{
		Ciphertexts: &s, SubmittedAt: &at, ExpiresAt: types.DateTime(expiresAt), ID: id, Now: at,
	})
	return n > 0, err
}

// Claim takes a submitted request for claimID.
func (r *Repo) Claim(ctx context.Context, id, claimID string, now, claimedUntil time.Time) (bool, error) {
	until := types.DateTime(claimedUntil)
	n, err := r.q.ClaimSecretRequest(ctx, r.tx.Querier(ctx), claimParams{
		ClaimID: &claimID, ClaimedUntil: &until, ID: id, Now: types.DateTime(now),
	})
	return n > 0, err
}

// Hold moves claimID's claim deadline.
func (r *Repo) Hold(ctx context.Context, id, claimID string, claimedUntil time.Time) (bool, error) {
	until := types.DateTime(claimedUntil)
	n, err := r.q.HoldSecretRequest(ctx, r.tx.Querier(ctx), holdParams{ClaimedUntil: &until, ID: id, ClaimID: &claimID})
	return n > 0, err
}

// MarkUsed ends a request claimed by claimID as used.
func (r *Repo) MarkUsed(ctx context.Context, id, claimID string, now time.Time) (bool, error) {
	at := types.DateTime(now)
	n, err := r.q.UseSecretRequest(ctx, r.tx.Querier(ctx), useParams{UsedAt: &at, ID: id, ClaimID: &claimID})
	return n > 0, err
}

// Release returns a request claimed by claimID to submitted.
func (r *Repo) Release(ctx context.Context, id, claimID string) (bool, error) {
	n, err := r.q.ReleaseSecretRequest(ctx, r.tx.Querier(ctx), releaseParams{ID: id, ClaimID: &claimID})
	return n > 0, err
}

// Expire ends the requests past their deadline.
func (r *Repo) Expire(ctx context.Context, now time.Time) (int64, error) {
	at := types.DateTime(now)
	return r.q.ExpireSecretRequests(ctx, r.tx.Querier(ctx), &at)
}

// Prune deletes the requests that ended before cutoff.
func (r *Repo) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	c := types.DateTime(cutoff)
	return r.q.PruneSecretRequests(ctx, r.tx.Querier(ctx), &c)
}

// hydrate maps a row (or its read error) to the model.
func hydrate(got row, err error) (model.SecretRequest, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return model.SecretRequest{}, model.ErrNotFound
	}
	if err != nil {
		return model.SecretRequest{}, err
	}
	req := model.SecretRequest{
		ID:        got.ID,
		OrgID:     got.OrgID,
		ServerID:  got.ServerID,
		CreatedBy: got.CreatedBy,
		Message:   got.Message,
		Status:    model.SecretRequestStatus(got.Status),
		CreatedAt: got.CreatedAt.Time(),
		ExpiresAt: got.ExpiresAt.Time(),
	}
	if got.AppID != nil {
		req.AppID = *got.AppID
	}
	if err := json.Unmarshal([]byte(got.Names), &req.Names); err != nil {
		return model.SecretRequest{}, fmt.Errorf("secret request %s: names: %w", got.ID, err)
	}
	if err := json.Unmarshal([]byte(got.Notes), &req.Notes); err != nil {
		return model.SecretRequest{}, fmt.Errorf("secret request %s: notes: %w", got.ID, err)
	}
	if got.Ciphertexts != nil {
		if err := json.Unmarshal([]byte(*got.Ciphertexts), &req.Ciphertexts); err != nil {
			return model.SecretRequest{}, fmt.Errorf("secret request %s: ciphertexts: %w", got.ID, err)
		}
	}
	req.SubmittedAt = timePtr(got.SubmittedAt)
	req.UsedAt = timePtr(got.UsedAt)
	req.EndedAt = timePtr(got.EndedAt)
	req.ClaimedUntil = timePtr(got.ClaimedUntil)
	return req, nil
}

func timePtr(d *types.DateTime) *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time()
	return &t
}
