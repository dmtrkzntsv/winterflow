package port

import (
	"context"
	"winterflow/internal/domain/model"
)

// AppRepository is the DB-backed catalog of apps (info, not live status). The
// agent's filesystem is the source of truth; the DB is a reconciled cache
// (see SyncApps + the apps.list command).
type AppRepository interface {
	GetApps(ctx context.Context, serverID string) ([]model.App, error)
	GetApp(ctx context.Context, appID string) (model.App, error)
	// SaveApp upserts the catalog record (create and edit share one path).
	SaveApp(ctx context.Context, app model.App) error
	DeleteApp(ctx context.Context, appID string) error
	RenameApp(ctx context.Context, appID, name string) error
	// SyncApps makes the DB mirror the agent's reported apps for a server:
	// upsert those present, remove those absent.
	SyncApps(ctx context.Context, serverID string, apps []model.App) error
}

// AppDomainRepository is the DB-backed ingress index: one row per hostname an app
// claims. The repository reconciles app config blobs via ReplaceForApp or
// ReplaceForServer; external APIs query for domain conflicts via FindClaims.
type AppDomainRepository interface {
	// FindClaims returns rows holding any of the given domains, excluding the
	// app being saved (its own claims are not conflicts). Claims carry app and
	// server names for the error message.
	FindClaims(ctx context.Context, domains []string, excludeAppID string) ([]model.DomainClaim, error)
	// ReplaceForApp makes the index mirror the app's ingress: delete the
	// app's rows, insert the new set, one transaction. nil ingress = no-op
	// (old clients); empty ingress = rows removed.
	ReplaceForApp(ctx context.Context, appID, serverID string, ing *model.Ingress) error
	DeleteForApp(ctx context.Context, appID string) error
	// ReplaceForServer rebuilds the whole server's rows from a reconciled app
	// list (apps carry .Ingress parsed from their config blobs).
	ReplaceForServer(ctx context.Context, serverID string, apps []model.App) error
	// ListForServer returns display rows grouped by app id.
	ListForServer(ctx context.Context, serverID string) (map[string][]model.AppDomainInfo, error)
}
