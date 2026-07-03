package service

import (
	"context"
	"winterflow/internal/domain/dto"
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

// ClaimServer materializes a server from a pending registration code into the
// caller's organization.
func (s *DbServerService) ClaimServer(ctx context.Context, d dto.ClaimServerDTO) (model.Server, error) {
	return s.repo.ClaimServer(ctx, d)
}

func (s *DbServerService) PendingRegistrationCode(ctx context.Context) (string, bool, error) {
	return s.repo.PendingRegistrationCode(ctx)
}
