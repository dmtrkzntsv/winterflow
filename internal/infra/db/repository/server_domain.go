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
	return listServerDomains(ctx, r.db.GetDB(), serverID)
}

func listServerDomains(ctx context.Context, db bun.IDB, serverID string) ([]model.RegisteredDomain, error) {
	var rows []models.ServerDomain
	if err := db.NewSelect().Model(&rows).
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

// ReplaceForServer makes the index mirror a domain.list answer. An answer
// equal to the stored rows (the common case: every agent reconnect sends
// one) writes nothing.
func (r *DbServerDomainRepository) ReplaceForServer(ctx context.Context, serverID string, ds []model.RegisteredDomain) error {
	return r.db.GetDB().RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		current, err := listServerDomains(ctx, tx, serverID)
		if err != nil {
			return err
		}
		if sameDomains(current, ds) {
			return nil
		}
		if _, err := tx.NewDelete().Model((*models.ServerDomain)(nil)).Where("server_id = ?", serverID).Exec(ctx); err != nil {
			return err
		}
		if len(ds) == 0 {
			return nil
		}
		rows := make([]models.ServerDomain, 0, len(ds))
		for _, d := range ds {
			rows = append(rows, serverDomainRow(serverID, d))
		}
		_, err = tx.NewInsert().Model(&rows).Exec(ctx)
		return err
	})
}

// sameDomains reports whether two registration lists hold the same rows,
// in any order (expiries compared to the second, as stored).
func sameDomains(a, b []model.RegisteredDomain) bool {
	if len(a) != len(b) {
		return false
	}
	byName := make(map[string]model.RegisteredDomain, len(a))
	for _, d := range a {
		byName[d.Name] = d
	}
	for _, d := range b {
		o, ok := byName[d.Name]
		if !ok || o.TLS != d.TLS || o.Issuer != d.Issuer || (o.NotAfter == nil) != (d.NotAfter == nil) ||
			(o.NotAfter != nil && !o.NotAfter.Truncate(time.Second).Equal(d.NotAfter.Truncate(time.Second))) {
			return false
		}
	}
	return true
}
