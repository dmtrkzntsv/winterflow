package port

import (
	"context"
	"winterflow/internal/domain/model"
)

type AppRepository interface {
	GetApps(ctx context.Context, serverID string) ([]model.App, error)
}

type AppService interface {
	GetApps(ctx context.Context, serverID string) ([]model.App, error)
	CreateApp(ctx context.Context, serverID string, app model.App, callback func(app model.App, err error)) error
}
