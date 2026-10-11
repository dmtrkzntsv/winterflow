package repository

import (
	"context"
	"sort"
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

// FindClaims selects only what a claim reports: the row's own columns plus
// the app's name (one join).
func (r *DbAppDomainRepository) FindClaims(ctx context.Context, serverID string, domains []string, excludeAppID string) ([]model.DomainClaim, error) {
	if len(domains) == 0 {
		return nil, nil
	}
	var claims []model.DomainClaim
	q := r.db.GetDB().NewSelect().
		TableExpr("app_domains AS d").
		ColumnExpr("d.domain, d.app_id, d.server_id").
		ColumnExpr("COALESCE(a.name, '') AS app_name").
		Join("LEFT JOIN apps AS a ON a.app_id = d.app_id").
		Where("d.server_id = ?", serverID).
		Where("d.domain IN (?)", bun.In(domains)).
		OrderExpr("d.domain ASC")
	if excludeAppID != "" {
		q = q.Where("d.app_id != ?", excludeAppID)
	}
	if err := q.Scan(ctx, &claims); err != nil {
		return nil, err
	}
	return claims, nil
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

// ReplaceForServer rebuilds the whole server's index from the agent's
// reported apps (apps.list reconcile). (server_id, domain) is the PK, so two
// apps claiming the same domain would collide on INSERT and roll back the
// whole reconcile, leaving the table stale forever with no way to self-heal.
// The agent can legitimately reach that state for a moment (BuildConfig in
// internal/infra/ingress/caddy/config.go accepts a concurrent-save race and
// resolves it by processing apps in sorted-AppID order, first claim wins).
// Dedup here the same way so the DB index always agrees with what Caddy is
// actually serving, and the INSERT never collides.
func (r *DbAppDomainRepository) ReplaceForServer(ctx context.Context, serverID string, apps []model.App) error {
	sorted := make([]model.App, len(apps))
	copy(sorted, apps)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	var rows []models.AppDomain
	claimed := map[string]bool{}
	for _, a := range sorted {
		for _, row := range rowsFor(a.ID, serverID, a.Ingress) {
			if claimed[row.Domain] {
				continue
			}
			claimed[row.Domain] = true
			rows = append(rows, row)
		}
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

// ListForApp returns the hostnames appID holds on serverID.
func (r *DbAppDomainRepository) ListForApp(ctx context.Context, serverID, appID string) ([]string, error) {
	var hosts []string
	err := r.db.GetDB().NewSelect().
		Model((*models.AppDomain)(nil)).
		Column("domain").
		Where("server_id = ?", serverID).
		Where("app_id = ?", appID).
		Order("domain ASC").
		Scan(ctx, &hosts)
	return hosts, err
}
