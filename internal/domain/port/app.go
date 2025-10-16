package port

import "winterflow/internal/domain/model"

type AppRepository interface {
	GetApps(serverID string) ([]model.App, error)
}
