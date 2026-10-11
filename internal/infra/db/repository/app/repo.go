// Package app implements port.AppRepository: the DB catalog of apps, a
// reconciled cache of what each agent reports (the agent filesystem is the
// source of truth).
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

type (
	appRow       = sqlitegen.App
	upsertParams = sqlitegen.UpsertAppParams
	renameParams = sqlitegen.RenameAppParams
)

type querier interface {
	ListAppsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]appRow, error)
	ListAppIDsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]string, error)
	GetApp(ctx context.Context, db backend.DBTX, appID string) (appRow, error)
	UpsertApp(ctx context.Context, db backend.DBTX, p upsertParams) error
	DeleteApp(ctx context.Context, db backend.DBTX, appID string) error
	RenameApp(ctx context.Context, db backend.DBTX, p renameParams) error
	DeleteMetricSamplesByApp(ctx context.Context, db backend.DBTX, appID string) error
}

// Repo is the apps repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.AppRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("app repository: unknown database driver %q", driver)
}

// GetApps lists a server's apps by created_at, ties (second precision)
// broken by the time-ordered UUIDv7 id so both engines agree.
func (r *Repo) GetApps(ctx context.Context, serverID string) ([]model.App, error) {
	rows, err := r.q.ListAppsByServer(ctx, r.tx.Querier(ctx), serverID)
	if err != nil {
		return nil, err
	}
	out := make([]model.App, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrate(row))
	}
	return out, nil
}

// GetApp returns one app; model.ErrAppNotFound when there is none.
func (r *Repo) GetApp(ctx context.Context, appID string) (model.App, error) {
	row, err := r.q.GetApp(ctx, r.tx.Querier(ctx), appID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.App{}, model.ErrAppNotFound
		}
		return model.App{}, err
	}
	return hydrate(row), nil
}

// SaveApp upserts the catalog record. An existing row keeps its server_id
// and created_at; name, version, icon, color and template_id are replaced.
func (r *Repo) SaveApp(ctx context.Context, a model.App) error {
	return r.q.UpsertApp(ctx, r.tx.Querier(ctx), upsertFor(a))
}

// DeleteApp removes the app row and its monitoring samples in one
// transaction (no error when it does not exist).
func (r *Repo) DeleteApp(ctx context.Context, appID string) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		return r.deleteApp(ctx, appID)
	})
}

// deleteApp drops the app's samples, then the row; ctx must carry the
// caller's transaction. An empty id is a no-op: app_id = '' marks the
// servers' own metric rows.
func (r *Repo) deleteApp(ctx context.Context, appID string) error {
	if appID == "" {
		return nil
	}
	if err := r.q.DeleteMetricSamplesByApp(ctx, r.tx.Querier(ctx), appID); err != nil {
		return err
	}
	return r.q.DeleteApp(ctx, r.tx.Querier(ctx), appID)
}

// RenameApp sets the app's name (no error when it does not exist).
func (r *Repo) RenameApp(ctx context.Context, appID, name string) error {
	return r.q.RenameApp(ctx, r.tx.Querier(ctx), renameParams{Name: name, AppID: appID})
}

// SyncApps makes the DB mirror the agent's reported apps for a server:
// upsert the reported ones and delete the server's rows the agent no longer
// has, in one transaction.
func (r *Repo) SyncApps(ctx context.Context, serverID string, apps []model.App) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		keep := make(map[string]bool, len(apps))
		for _, a := range apps {
			a.ServerID = serverID
			if err := r.q.UpsertApp(ctx, r.tx.Querier(ctx), upsertFor(a)); err != nil {
				return err
			}
			keep[a.ID] = true
		}
		ids, err := r.q.ListAppIDsByServer(ctx, r.tx.Querier(ctx), serverID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if keep[id] {
				continue
			}
			if err := r.deleteApp(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// upsertFor maps an app to its row: a zero CreatedAt becomes now, an empty
// TemplateID NULL.
func upsertFor(a model.App) upsertParams {
	p := upsertParams{
		AppID:     a.ID,
		ServerID:  a.ServerID,
		Name:      a.Name,
		Version:   a.Version,
		Icon:      a.Icon,
		Color:     a.Color,
		CreatedAt: types.DateTime(a.CreatedAt),
	}
	if a.CreatedAt.IsZero() {
		p.CreatedAt = types.NewDateTime()
	}
	if a.TemplateID != "" {
		id := a.TemplateID
		p.TemplateID = &id
	}
	return p
}

func hydrate(row appRow) model.App {
	a := model.App{
		ID:        row.AppID,
		ServerID:  row.ServerID,
		Version:   row.Version,
		Name:      row.Name,
		Icon:      row.Icon,
		Color:     row.Color,
		CreatedAt: row.CreatedAt.Time(),
	}
	if row.TemplateID != nil {
		a.TemplateID = *row.TemplateID
	}
	return a
}
