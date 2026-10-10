package port

import (
	"context"
	"winterflow/internal/domain/command"
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
	// FindClaims returns rows on serverID holding any of the given domains,
	// excluding the app being saved (its own claims are not conflicts). A
	// hostname is unique per server, so other servers' claims never count.
	// Claims carry app and server names for the error message.
	FindClaims(ctx context.Context, serverID string, domains []string, excludeAppID string) ([]model.DomainClaim, error)
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

// ServerDomainRepository is the DB-backed index of each server's domain
// registrations (the agent's domains/ dir is the source of truth): what
// save-app checks ingress hostnames against.
type ServerDomainRepository interface {
	// ListForServer returns the server's registrations sorted by name.
	ListForServer(ctx context.Context, serverID string) ([]model.RegisteredDomain, error)
	// Upsert records a registration the agent confirmed creating.
	Upsert(ctx context.Context, serverID string, d model.RegisteredDomain) error
	// Delete drops a registration the agent confirmed deleting.
	Delete(ctx context.Context, serverID, name string) error
	// ReplaceForServer makes the index mirror a domain.list answer.
	ReplaceForServer(ctx context.Context, serverID string, ds []model.RegisteredDomain) error
}

// IngressManager owns the embedded reverse proxy. Implementations must be
// safe for concurrent use and must never let an ingress failure propagate as
// a command failure.
type IngressManager interface {
	// Reload rebuilds the merged config from the apps on disk and hot-swaps
	// it. Returned strings are warnings for the triggering command's
	// response; a load failure keeps the previous config serving. force
	// reloads even when the config is unchanged — needed after replacing a
	// certificate, whose file paths (and so the config) stay the same.
	Reload(ctx context.Context, force bool) []string
	// Enabled reports whether the proxy bound its ports at startup.
	Enabled() bool
	// ListDomains reports the domain registrations with the apps using
	// each; it works with ingress disabled.
	ListDomains(ctx context.Context) ([]command.Domain, error)
	// CreateDomain registers or replaces a name (certificate mode: the
	// ECIES-encrypted key is decrypted and the pair checked first).
	CreateDomain(ctx context.Context, in command.CreateDomainRequest) (command.Domain, error)
	// DeleteDomain removes a registration no app uses.
	DeleteDomain(ctx context.Context, name string) error
}
