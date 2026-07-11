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

func NewDbAppDomainRepository(db *db.BunConnection, log *logger.Logger) *DbAppDomainRepository {
	return &DbAppDomainRepository{db: db, log: log}
}

// DbAppDomainRepository maintains the app_domains index. The agent filesystem
// stays authoritative; every method here is cache maintenance.
type DbAppDomainRepository struct {
	db  *db.BunConnection
	log *logger.Logger
}

// rowsFor flattens an ingress into index rows: routes plus domain-level
// redirect sources. Path rules produce no rows (they cannot conflict).
func rowsFor(appID, serverID string, ing *model.Ingress) []models.AppDomain {
	if ing == nil {
		return nil
	}
	now := types.DateTime(time.Now().UTC())
	var rows []models.AppDomain
	for _, d := range ing.Domains {
		rows = append(rows, models.AppDomain{
			Domain: d.Domain, AppID: appID, ServerID: serverID,
			Kind: "route", SSL: d.SSL, UpstreamPort: d.UpstreamPort, UpdatedAt: now,
		})
	}
	for _, r := range ing.Redirects {
		if r.Path != "" {
			continue
		}
		rows = append(rows, models.AppDomain{
			Domain: r.Domain, AppID: appID, ServerID: serverID,
			Kind: "redirect", SSL: r.SSL, Target: r.To, Code: r.Code, UpdatedAt: now,
		})
	}
	return rows
}

func (r *DbAppDomainRepository) FindClaims(ctx context.Context, domains []string, excludeAppID string) ([]model.DomainClaim, error) {
	if len(domains) == 0 {
		return nil, nil
	}
	var rows []models.AppDomain
	q := r.db.GetDB().NewSelect().
		Model(&rows).
		Relation("App").
		Relation("Server").
		Where("app_domain.domain IN (?)", bun.In(domains))
	if excludeAppID != "" {
		q = q.Where("app_domain.app_id != ?", excludeAppID)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]model.DomainClaim, 0, len(rows))
	for _, row := range rows {
		claim := model.DomainClaim{Domain: row.Domain, AppID: row.AppID, ServerID: row.ServerID}
		if row.App != nil {
			claim.AppName = row.App.Name
		}
		if row.Server != nil {
			claim.ServerName = row.Server.Name
		}
		out = append(out, claim)
	}
	return out, nil
}

func (r *DbAppDomainRepository) ReplaceForApp(ctx context.Context, appID, serverID string, ing *model.Ingress) error {
	if ing == nil {
		// Old client / config without the key: leave the index alone.
		return nil
	}
	rows := rowsFor(appID, serverID, ing)
	return r.db.GetDB().RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*models.AppDomain)(nil)).Where("app_id = ?", appID).Exec(ctx); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		_, err := tx.NewInsert().Model(&rows).Exec(ctx)
		return err
	})
}

func (r *DbAppDomainRepository) DeleteForApp(ctx context.Context, appID string) error {
	_, err := r.db.GetDB().NewDelete().Model((*models.AppDomain)(nil)).Where("app_id = ?", appID).Exec(ctx)
	return err
}

func (r *DbAppDomainRepository) ReplaceForServer(ctx context.Context, serverID string, apps []model.App) error {
	var rows []models.AppDomain
	for _, a := range apps {
		rows = append(rows, rowsFor(a.ID, serverID, a.Ingress)...)
	}
	return r.db.GetDB().RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*models.AppDomain)(nil)).Where("server_id = ?", serverID).Exec(ctx); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		_, err := tx.NewInsert().Model(&rows).Exec(ctx)
		return err
	})
}

func (r *DbAppDomainRepository) ListForServer(ctx context.Context, serverID string) (map[string][]model.AppDomainInfo, error) {
	var rows []models.AppDomain
	err := r.db.GetDB().NewSelect().
		Model(&rows).
		Where("server_id = ?", serverID).
		Order("domain ASC").
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]model.AppDomainInfo{}
	for _, row := range rows {
		out[row.AppID] = append(out[row.AppID], model.AppDomainInfo{Domain: row.Domain, SSL: row.SSL, Kind: row.Kind})
	}
	return out, nil
}
