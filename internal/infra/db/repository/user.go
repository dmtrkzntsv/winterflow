package repository

import (
	"context"
	"database/sql"
	"errors"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	genq "winterflow/internal/infra/db/generated"
	"winterflow/pkg/logger"
)

func NewDbUserRepository(db *db.Connection, log *logger.Logger) *DbUserRepository {
	return &DbUserRepository{
		db:  db,
		log: log,
	}
}

type DbUserRepository struct {
	db  *db.Connection
	log *logger.Logger
}

func (r *DbUserRepository) GetByConnectedAccount(ctx context.Context, provider, accountID string) (model.User, error) {
	repo, err := r.db.Repo(ctx)
	if err != nil {
		return model.User{}, err
	}
	defer repo.Close()

	acc, err := repo.GetUserConnectedAccount(ctx, genq.GetUserConnectedAccountParams{
		Provider:   provider,
		ExternalID: accountID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, model.ErrorUserNotFound
		}
		return model.User{}, err
	}

	user, err := repo.GetUser(ctx, acc.UserID)
	if err != nil {
		return model.User{}, err
	}

	return model.User{
		ID:         user.UserID.(string),
		Name:       user.Name,
		AvatarURL:  user.Avatar.String,
		CreatedAt:  user.CreatedAt.Time(),
		LastSeenAt: user.LastSeen.Time(),
	}, nil
}

func (r *DbUserRepository) CreateUser(ctx context.Context, dto dto.UserDTO) (model.User, error) {
	err := r.db.Transaction(ctx, func(repo *genq.Queries) error {
		_, err := repo.CreateUser(ctx, genq.CreateUserParams{
			UserID: dto.UserID,
			Name:   dto.Name,
			Avatar: sql.NullString{String: dto.AvatarURL, Valid: dto.AvatarURL != ""},
		})
		if err != nil {
			return err
		}

		_, err = repo.CreateUserConnectedAccount(ctx, genq.CreateUserConnectedAccountParams{
			Provider:   dto.Provider,
			ExternalID: dto.AccountID,
			UserID:     dto.UserID,
		})
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return model.User{}, err
	}

	repo, err := r.db.Repo(ctx)
	if err != nil {
		return model.User{}, err
	}
	defer repo.Close()

	u, err := repo.GetUser(ctx, dto.UserID)
	if err != nil {
		return model.User{}, err
	}
	return model.User{
		ID:         u.UserID.(string),
		Name:       u.Name,
		AvatarURL:  u.Avatar.String,
		CreatedAt:  u.CreatedAt.Time(),
		LastSeenAt: u.LastSeen.Time(),
	}, nil
}

func (r *DbUserRepository) GetUser(ctx context.Context, userID string) (model.User, error) {
	repo, err := r.db.Repo(ctx)
	if err != nil {
		return model.User{}, err
	}
	defer repo.Close()

	user, err := repo.GetUser(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, model.ErrorUserNotFound
		}
		return model.User{}, err
	}

	return model.User{
		ID:         user.UserID.(string),
		Name:       user.Name,
		AvatarURL:  user.Avatar.String,
		CreatedAt:  user.CreatedAt.Time(),
		LastSeenAt: user.LastSeen.Time(),
	}, nil
}
