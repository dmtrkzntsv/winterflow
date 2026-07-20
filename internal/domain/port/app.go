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
