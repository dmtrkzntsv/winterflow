package port

import (
	"context"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
)

type UserRepository interface {
	GetByConnectedAccount(ctx context.Context, provider, accountID string) (model.User, error)
	CreateUser(ctx context.Context, dto dto.UserDTO) (model.User, error)
	GetUser(ctx context.Context, userID string) (model.User, error)
}

type UserService interface {
	FindOrCreateUser(ctx context.Context, dto dto.UserDTO) (model.User, error)
}
