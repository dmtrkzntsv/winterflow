package service

import (
	"context"
	"errors"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	repo "winterflow/internal/infra/db/repository"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
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
		if dto.UserID == "" {
			dto.UserID = util.GenerateID()
		}
		cu, err := s.repo.CreateUser(ctx, dto)
		if err != nil {
			s.log.Error("failed to create user: %v", err)
		}
		return cu, nil
	}

	return user, nil
}
