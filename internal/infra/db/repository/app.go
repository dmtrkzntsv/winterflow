package repository

import (
	"context"
	"database/sql"
	"errors"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/models"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"

	"github.com/uptrace/bun"
)

func NewDbAppRepository(db *db.BunConnection, log *logger.Logger) *DbAppRepository {
	return &DbAppRepository{db: db, log: log}
}

type DbAppRepository struct {
	db  *db.BunConnection
	log *logger.Logger
}

func (r *DbAppRepository) GetApps(ctx context.Context, serverID string) ([]model.App, error) {
	var rows []models.App
	err := r.db.GetDB().NewSelect().
		Model(&rows).
		Where("server_id = ?", serverID).
		Order("created_at ASC").
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.App, 0, len(rows))
	for i := range rows {
		out = append(out, toDomainApp(&rows[i]))
	}
	return out, nil
}

func (r *DbAppRepository) GetApp(ctx context.Context, appID string) (model.App, error) {
	var row models.App
	err := r.db.GetDB().NewSelect().
		Model(&row).
		Where("app_id = ?", appID).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.App{}, model.ErrAppNotFound
		}
		return model.App{}, err
	}
	return toDomainApp(&row), nil
}

func (r *DbAppRepository) CreateApp(ctx context.Context, app model.App) error {
	row := toDBApp(app)
	if row.CreatedAt.Time().IsZero() {
		row.CreatedAt = types.NewDateTime()
	}
	_, err := r.db.GetDB().NewInsert().
		Model(row).
		On("CONFLICT (app_id) DO UPDATE").
		Set("name = EXCLUDED.name").
		Set("version = EXCLUDED.version").
		Set("icon = EXCLUDED.icon").
		Set("color = EXCLUDED.color").
		Set("template_id = EXCLUDED.template_id").
		Exec(ctx)
	return err
}

func (r *DbAppRepository) DeleteApp(ctx context.Context, appID string) error {
	_, err := r.db.GetDB().NewDelete().
		Model((*models.App)(nil)).
		Where("app_id = ?", appID).
		Exec(ctx)
	return err
}

func (r *DbAppRepository) RenameApp(ctx context.Context, appID, name string) error {
	_, err := r.db.GetDB().NewUpdate().
		Model((*models.App)(nil)).
		Set("name = ?", name).
		Where("app_id = ?", appID).
		Exec(ctx)
	return err
}

// SyncApps makes the DB mirror the agent's reported apps for a server: upsert
// the reported ones and delete any DB rows the agent no longer has.
func (r *DbAppRepository) SyncApps(ctx context.Context, serverID string, apps []model.App) error {
	return r.db.Transaction(ctx, func(tx bun.IDB) error {
		keep := make([]string, 0, len(apps))
		for _, a := range apps {
			a.ServerID = serverID
			row := toDBApp(a)
			if row.CreatedAt.Time().IsZero() {
				row.CreatedAt = types.NewDateTime()
			}
			if _, err := tx.NewInsert().
				Model(row).
				On("CONFLICT (app_id) DO UPDATE").
				Set("name = EXCLUDED.name").
				Set("version = EXCLUDED.version").
				Set("icon = EXCLUDED.icon").
				Set("color = EXCLUDED.color").
				Set("template_id = EXCLUDED.template_id").
				Exec(ctx); err != nil {
				return err
			}
			keep = append(keep, a.ID)
		}

		del := tx.NewDelete().Model((*models.App)(nil)).Where("server_id = ?", serverID)
		if len(keep) > 0 {
			del = del.Where("app_id NOT IN (?)", bun.In(keep))
		}
		_, err := del.Exec(ctx)
		return err
	})
}

func toDomainApp(a *models.App) model.App {
	app := model.App{
		ID:        a.AppID,
		ServerID:  a.ServerID,
		Version:   a.Version,
		Name:      a.Name,
		Icon:      a.Icon,
		Color:     a.Color,
		CreatedAt: a.CreatedAt.Time(),
	}
	if a.TemplateID != nil {
		app.TemplateID = *a.TemplateID
	}
	return app
}

func toDBApp(a model.App) *models.App {
	row := &models.App{
		AppID:     a.ID,
		ServerID:  a.ServerID,
		Name:      a.Name,
		Version:   a.Version,
		Icon:      a.Icon,
		Color:     a.Color,
		CreatedAt: types.DateTime(a.CreatedAt),
	}
	if a.TemplateID != "" {
		row.TemplateID = util.RefString(a.TemplateID)
	}
	return row
}
