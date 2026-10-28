// Package cmdresult implements port.CommandResultRepository: the results of
// the commands MCP tools dispatch.
package cmdresult

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
	row          = sqlitegen.CommandResult
	insertParams = sqlitegen.InsertCommandResultParams
	finishParams = sqlitegen.FinishCommandResultParams
	getParams    = sqlitegen.GetCommandResultParams
	staleParams  = sqlitegen.MarkStaleCommandResultsParams
)

type querier interface {
	InsertCommandResult(ctx context.Context, db backend.DBTX, p insertParams) error
	FinishCommandResult(ctx context.Context, db backend.DBTX, p finishParams) (int64, error)
	GetCommandResult(ctx context.Context, db backend.DBTX, p getParams) (row, error)
	MarkStaleCommandResults(ctx context.Context, db backend.DBTX, p staleParams) (int64, error)
	PruneCommandResults(ctx context.Context, db backend.DBTX, cutoff *types.DateTime) (int64, error)
}

// Repo is the command results repository.
type Repo struct {
	tx *backend.TxManager
	q  querier
}

var _ port.CommandResultRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}}, nil
	}
	return nil, fmt.Errorf("command result repository: unknown database driver %q", driver)
}

// Create inserts rec as given (its status is normally running).
func (r *Repo) Create(ctx context.Context, rec model.CommandRecord) error {
	return r.q.InsertCommandResult(ctx, r.tx.Querier(ctx), insertParams{
		RequestID: rec.RequestID,
		OrgID:     rec.OrgID,
		ServerID:  rec.ServerID,
		Kind:      rec.Kind,
		Status:    string(rec.Status),
		CreatedAt: types.DateTime(rec.CreatedAt),
	})
}

// Finish records an outcome on a running or unknown row.
func (r *Repo) Finish(ctx context.Context, requestID string, status model.CommandStatus, result []byte, errText string, finishedAt time.Time) (bool, error) {
	at := types.DateTime(finishedAt)
	p := finishParams{Status: string(status), FinishedAt: &at, RequestID: requestID}
	if result != nil {
		s := string(result)
		p.Result = &s
	}
	if errText != "" {
		p.Error = &errText
	}
	n, err := r.q.FinishCommandResult(ctx, r.tx.Querier(ctx), p)
	return n > 0, err
}

// Get reads the org's record.
func (r *Repo) Get(ctx context.Context, orgID, requestID string) (model.CommandRecord, error) {
	got, err := r.q.GetCommandResult(ctx, r.tx.Querier(ctx), getParams{RequestID: requestID, OrgID: orgID})
	if errors.Is(err, sql.ErrNoRows) {
		return model.CommandRecord{}, model.ErrNotFound
	}
	if err != nil {
		return model.CommandRecord{}, err
	}
	rec := model.CommandRecord{
		RequestID: got.RequestID,
		OrgID:     got.OrgID,
		ServerID:  got.ServerID,
		Kind:      got.Kind,
		Status:    model.CommandStatus(got.Status),
		CreatedAt: got.CreatedAt.Time(),
	}
	if got.Result != nil {
		rec.Result = []byte(*got.Result)
	}
	if got.Error != nil {
		rec.Error = *got.Error
	}
	if got.FinishedAt != nil {
		t := got.FinishedAt.Time()
		rec.FinishedAt = &t
	}
	return rec, nil
}

// MarkStale turns running rows created before cutoff into unknown ones.
func (r *Repo) MarkStale(ctx context.Context, cutoff, now time.Time) (int64, error) {
	at := types.DateTime(now)
	return r.q.MarkStaleCommandResults(ctx, r.tx.Querier(ctx), staleParams{FinishedAt: &at, CreatedAt: types.DateTime(cutoff)})
}

// Prune deletes rows that ended before cutoff.
func (r *Repo) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	c := types.DateTime(cutoff)
	return r.q.PruneCommandResults(ctx, r.tx.Querier(ctx), &c)
}
