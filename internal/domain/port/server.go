package port

import (
	"context"
	"winterflow/internal/domain/model"
)

type ServerRepository interface {
	GetServers(ctx context.Context, userID string) ([]model.Server, error)
}

type ServerService interface {
	GetServers(ctx context.Context, userID string) ([]model.Server, error)
}
