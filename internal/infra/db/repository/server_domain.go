package repository

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/models"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"

	"github.com/uptrace/bun"
)

func NewDbServerDomainRepository(db *db.BunConnection, log *logger.Logger) *DbServerDomainRepository {
	return &DbServerDomainRepository{db: db, log: log}
}

// DbServerDomainRepository maintains the server_domains index. The agent's
// domains/ dir stays authoritative; every method here is cache maintenance.
type DbServerDomainRepository struct {
	db  *db.BunConnection
	log *logger.Logger
}

func serverDomainRow(serverID string, d model.RegisteredDomain) models.ServerDomain {
	row := models.ServerDomain{
		ServerID: serverID, Name: d.Name, TLS: string(d.TLS), Issuer: d.Issuer,
		UpdatedAt: types.DateTime(time.Now().UTC()),
	}
	if d.NotAfter != nil {
		na := types.DateTime(d.NotAfter.UTC())
		row.NotAfter = &na
	}
	return row
}

// ListForServer returns the server's registrations sorted by name.
func (r *DbServerDomainRepository) ListForServer(ctx context.Context, serverID string) ([]model.RegisteredDomain, error) {
	var rows []models.ServerDomain
	if err := r.db.GetDB().NewSelect().Model(&rows).
		Where("server_id = ?", serverID).Order("name ASC").Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]model.RegisteredDomain, 0, len(rows))
	for _, row := range rows {
		d := model.RegisteredDomain{Name: row.Name, TLS: model.DomainTLS(row.TLS), Issuer: row.Issuer}
		if row.NotAfter != nil {
			na := row.NotAfter.Time().UTC()
			d.NotAfter = &na
		}
		out = append(out, d)
	}
	return out, nil
}

// Upsert records one registration (domain.create confirmed by the agent).
func (r *DbServerDomainRepository) Upsert(ctx context.Context, serverID string, d model.RegisteredDomain) error {
	row := serverDomainRow(serverID, d)
	_, err := r.db.GetDB().NewInsert().Model(&row).
		On("CONFLICT (server_id, name) DO UPDATE").
		Set("tls = EXCLUDED.tls").
		Set("not_after = EXCLUDED.not_after").
		Set("issuer = EXCLUDED.issuer").
		Set("updated_at = EXCLUDED.updated_at").
		Exec(ctx)
	return err
}

// Delete drops one registration (domain.delete confirmed by the agent).
func (r *DbServerDomainRepository) Delete(ctx context.Context, serverID, name string) error {
	_, err := r.db.GetDB().NewDelete().Model((*models.ServerDomain)(nil)).
		Where("server_id = ?", serverID).Where("name = ?", name).Exec(ctx)
	return err
}

// ReplaceForServer makes the index mirror a domain.list answer.
func (r *DbServerDomainRepository) ReplaceForServer(ctx context.Context, serverID string, ds []model.RegisteredDomain) error {
	rows := make([]models.ServerDomain, 0, len(ds))
	for _, d := range ds {
		rows = append(rows, serverDomainRow(serverID, d))
	}
	return r.db.GetDB().RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*models.ServerDomain)(nil)).Where("server_id = ?", serverID).Exec(ctx); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		_, err := tx.NewInsert().Model(&rows).Exec(ctx)
		return err
	})
}
