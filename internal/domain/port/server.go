package port

import (
	"context"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
)

type ServerRepository interface {
	GetServers(ctx context.Context, userID string) ([]model.Server, error)
	AddServer(ctx context.Context, dto dto.ServerDTO) (model.Server, error)
	RegisterServer(ctx context.Context, dto dto.ServerRegistrationDTO) error
	IsServerRegistered(ctx context.Context, serverID string) (bool, error)
}

type ServerService interface {
	GetServers(ctx context.Context, userID string) ([]model.Server, error)
	AddServer(ctx context.Context, dto dto.ServerDTO, callback func(app model.Server, err error)) error
}
