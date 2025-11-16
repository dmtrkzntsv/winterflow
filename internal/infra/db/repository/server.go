package repository

import (
	"context"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/pkg/logger"
)

func NewDbServerRepository(db *db.BunConnection, log *logger.Logger) *DbServerRepository {
	return &DbServerRepository{
		db:  db,
		log: log,
	}
}

type DbServerRepository struct {
	db  *db.BunConnection
	log *logger.Logger
}

func (r *DbServerRepository) GetServers(ctx context.Context, userID string) ([]model.Server, error) {
	return make([]model.Server, 0), nil
}

func (r *DbServerRepository) AddServer(ctx context.Context, dto dto.ServerDTO) (model.Server, error) {
	return model.Server{}, nil
}
