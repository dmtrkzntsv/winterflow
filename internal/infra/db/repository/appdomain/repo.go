// Package appdomain implements port.AppDomainRepository: the app_domains
// ingress index, one row per hostname an app claims. The agent filesystem
// stays authoritative; every method here is cache maintenance.
package appdomain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

type (
	domainRow    = sqlitegen.AppDomain
	claimRow     = sqlitegen.GetDomainClaimRow
	claimParams  = sqlitegen.GetDomainClaimParams
	insertParams = sqlitegen.InsertAppDomainParams
	namesParams  = sqlitegen.ListAppDomainNamesParams
)

type querier interface {
	GetDomainClaim(ctx context.Context, db backend.DBTX, p claimParams) (claimRow, error)
	InsertAppDomain(ctx context.Context, db backend.DBTX, p insertParams) error
	DeleteAppDomainsByApp(ctx context.Context, db backend.DBTX, appID string) error
	DeleteAppDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) error
	ListAppDomainsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]domainRow, error)
	ListAppDomainNames(ctx context.Context, db backend.DBTX, p namesParams) ([]string, error)
}

// Repo is the app_domains repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.AppDomainRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("appdomain repository: unknown database driver %q", driver)
}

// rowsFor flattens an ingress into index rows: routes plus domain-level
// redirect sources. Path rules produce no rows (they cannot conflict).
func rowsFor(appID, serverID string, ing *model.Ingress) []insertParams {
	if ing == nil {
		return nil
	}
	now := types.DateTime(time.Now().UTC())
	var rows []insertParams
	for _, d := range ing.Domains {
		rows = append(rows, insertParams{
			Domain: d.Domain, AppID: appID, ServerID: serverID,
			Kind: "route", Ssl: d.SSL, UpstreamPort: int64(d.UpstreamPort), UpdatedAt: now,
		})
	}
	for _, r := range ing.Redirects {
		if r.Path != "" {
			continue
		}
		rows = append(rows, insertParams{
			Domain: r.Domain, AppID: appID, ServerID: serverID,
			Kind: "redirect", Ssl: r.SSL, Target: r.To, Code: int64(r.Code), UpdatedAt: now,
		})
	}
	return rows
}

// FindClaims returns the rows on serverID holding any of domains, minus
// excludeAppID's own, sorted by domain, each with its app's name ("" when
// the app row is gone). (server_id, domain) is the key, so each domain has
// at most one claim: one keyed lookup per distinct domain.
func (r *Repo) FindClaims(ctx context.Context, serverID string, domains []string, excludeAppID string) ([]model.DomainClaim, error) {
	if len(domains) == 0 {
		return nil, nil
	}
	uniq := make([]string, 0, len(domains))
	seen := make(map[string]bool, len(domains))
	for _, d := range domains {
		if !seen[d] {
			seen[d] = true
			uniq = append(uniq, d)
		}
	}
	sort.Strings(uniq)
	var claims []model.DomainClaim
	for _, d := range uniq {
		row, err := r.q.GetDomainClaim(ctx, r.tx.Querier(ctx), claimParams{ServerID: serverID, Domain: d})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if excludeAppID != "" && row.AppID == excludeAppID {
			continue
		}
		claims = append(claims, model.DomainClaim{Domain: row.Domain, AppID: row.AppID, AppName: row.AppName, ServerID: row.ServerID})
	}
	return claims, nil
}

// ReplaceForApp deletes the app's rows (on every server) and inserts the new
// set, in one transaction. nil ingress = no-op (old clients).
func (r *Repo) ReplaceForApp(ctx context.Context, appID, serverID string, ing *model.Ingress) error {
	if ing == nil {
		// Old client / config without the key: leave the index alone.
		return nil
	}
	rows := rowsFor(appID, serverID, ing)
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := r.q.DeleteAppDomainsByApp(ctx, r.tx.Querier(ctx), appID); err != nil {
			return err
		}
		return r.insert(ctx, rows)
	})
}

// DeleteForApp drops every row of the app.
func (r *Repo) DeleteForApp(ctx context.Context, appID string) error {
	return r.q.DeleteAppDomainsByApp(ctx, r.tx.Querier(ctx), appID)
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
func (r *Repo) ReplaceForServer(ctx context.Context, serverID string, apps []model.App) error {
	sorted := make([]model.App, len(apps))
	copy(sorted, apps)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	var rows []insertParams
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
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := r.q.DeleteAppDomainsByServer(ctx, r.tx.Querier(ctx), serverID); err != nil {
			return err
		}
		return r.insert(ctx, rows)
	})
}

func (r *Repo) insert(ctx context.Context, rows []insertParams) error {
	for _, row := range rows {
		if err := r.q.InsertAppDomain(ctx, r.tx.Querier(ctx), row); err != nil {
			return err
		}
	}
	return nil
}

// ListForServer returns display rows grouped by app id (always non-nil).
func (r *Repo) ListForServer(ctx context.Context, serverID string) (map[string][]model.AppDomainInfo, error) {
	rows, err := r.q.ListAppDomainsByServer(ctx, r.tx.Querier(ctx), serverID)
	if err != nil {
		return nil, err
	}
	out := map[string][]model.AppDomainInfo{}
	for _, row := range rows {
		out[row.AppID] = append(out[row.AppID], model.AppDomainInfo{Domain: row.Domain, SSL: row.Ssl, Kind: row.Kind})
	}
	return out, nil
}

// ListForApp returns the hostnames appID holds on serverID, sorted.
func (r *Repo) ListForApp(ctx context.Context, serverID, appID string) ([]string, error) {
	return r.q.ListAppDomainNames(ctx, r.tx.Querier(ctx), namesParams{ServerID: serverID, AppID: appID})
}
