package service

import (
	"context"
	"errors"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	repo "winterflow/internal/infra/db/repository"
	"winterflow/pkg/logger"
)

type DbUserService struct {
	repo *repo.DbUserRepository
	log  *logger.Logger
}

func NewDbUserService(log *logger.Logger, r *repo.DbUserRepository) *DbUserService {
	return &DbUserService{
		repo: r,
		log:  log,
	}
}

func (s *DbUserService) FindOrCreateUser(ctx context.Context, dto dto.UserDTO) (model.User, error) {
	user, err := s.repo.GetByConnectedAccount(ctx, dto.Provider, dto.AccountID)
	if err != nil {
		if !errors.Is(err, model.ErrorUserNotFound) {
			return model.User{}, err
		}
		cu, err := s.repo.CreateUser(ctx, dto)
		if err != nil {
			s.log.Error("failed to create user: %v", err)
			return model.User{}, err
		}
		return cu, nil
	}

	return user, nil
}

func (s *DbUserService) PrimaryOrganizationID(ctx context.Context, userID string) (string, error) {
	return s.repo.PrimaryOrganizationID(ctx, userID)
}

func (s *DbUserService) FindByToken(ctx context.Context, token string) (model.User, error) {
	return s.repo.FindByToken(ctx, token)
}
