package service

import (
	"context"
	"winterflow/internal/domain/model"
	repo "winterflow/internal/infra/db/repository"
	"winterflow/pkg/logger"
)

type DbServerService struct {
	repo *repo.DbServerRepository
	log  *logger.Logger
}

func NewDbServerService(log *logger.Logger, r *repo.DbServerRepository) *DbServerService {
	return &DbServerService{
		repo: r,
		log:  log,
	}
}

func (s *DbServerService) GetServers(ctx context.Context, userID string) ([]model.Server, error) {
	return s.repo.GetServers(ctx, userID)
}
