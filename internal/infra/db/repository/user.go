package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/models"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"

	"github.com/uptrace/bun"
)

func NewDbUserRepository(db *db.BunConnection, log *logger.Logger) *DbUserRepository {
	return &DbUserRepository{
		db:  db,
		log: log,
	}
}

type DbUserRepository struct {
	db  *db.BunConnection
	log *logger.Logger
}

func (r *DbUserRepository) GetByConnectedAccount(ctx context.Context, provider, accountID string) (model.User, error) {
	dbi := r.db.GetDB()

	// First find the connected account to get the user ID
	var connectedAccount models.UserConnectedAccount
	err := dbi.NewSelect().
		Model(&connectedAccount).
		Where("provider = ? AND external_id = ?", provider, accountID).
		Scan(ctx)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, model.ErrorUserNotFound
		}
		return model.User{}, err
	}

	// Then get the user by ID
	var user models.User
	err = dbi.NewSelect().
		Model(&user).
		Where("user_id = ?", connectedAccount.UserID).
		Scan(ctx)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, model.ErrorUserNotFound
		}
		return model.User{}, err
	}

	return model.User{
		ID:         user.UserID,
		Name:       user.Name,
		AvatarURL:  util.DerefString(user.Avatar),
		CreatedAt:  user.CreatedAt.Time(),
		LastSeenAt: user.LastSeen.Time(),
	}, nil
}

func (r *DbUserRepository) CreateUser(ctx context.Context, dto dto.UserDTO) (model.User, error) {
	org := &models.Organization{
		BaseModel:      bun.BaseModel{},
		OrganizationID: util.GenerateID(),
		Name:           strings.ToLower(dto.Name) + "'s org",
		CreatedAt:      util.NewDateTime(),
	}

	user := &models.User{
		UserID:    util.GenerateID(),
		Name:      dto.Name,
		Avatar:    util.RefString(dto.AvatarURL),
		CreatedAt: util.NewDateTime(),
		LastSeen:  util.NewDateTime(),
	}

	orgUser := &models.OrganizationUser{
		BaseModel:      bun.BaseModel{},
		OrganizationID: org.OrganizationID,
		UserID:         user.UserID,
		Role:           model.RoleOwner.Value(),
		CreatedAt:      util.NewDateTime(),
		Organization:   org,
		User:           user,
	}

	connectedAccount := &models.UserConnectedAccount{
		Provider:   dto.Provider,
		ExternalID: dto.AccountID,
		UserID:     user.UserID,
	}

	err := r.db.Transaction(ctx, func(tx bun.IDB) error {
		_, err := tx.NewInsert().Model(org).Exec(ctx)
		if err != nil {
			return err
		}

		_, err = tx.NewInsert().Model(user).Exec(ctx)
		if err != nil {
			return err
		}

		_, err = tx.NewInsert().Model(orgUser).Exec(ctx)
		if err != nil {
			return err
		}

		_, err = tx.NewInsert().Model(connectedAccount).Exec(ctx)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return model.User{}, err
	}

	return model.User{
		ID:         user.UserID,
		Name:       user.Name,
		AvatarURL:  util.DerefString(user.Avatar),
		CreatedAt:  user.CreatedAt.Time(),
		LastSeenAt: user.LastSeen.Time(),
	}, nil
}

func (r *DbUserRepository) GetUser(ctx context.Context, userID string) (model.User, error) {
	dbi := r.db.GetDB()

	var user models.User
	err := dbi.NewSelect().
		Model(&user).
		Where("user_id = ?", userID).
		Scan(ctx)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, model.ErrorUserNotFound
		}
		return model.User{}, err
	}

	return model.User{
		ID:         user.UserID,
		Name:       user.Name,
		AvatarURL:  util.DerefString(user.Avatar),
		CreatedAt:  user.CreatedAt.Time(),
		LastSeenAt: user.LastSeen.Time(),
	}, nil
}

// PrimaryOrganizationID returns the user's organization. The 1-user-per-org
// model gives each user exactly one (created at signup); if a user ever belongs
// to several, the oldest membership wins so the result is stable.
func (r *DbUserRepository) PrimaryOrganizationID(ctx context.Context, userID string) (string, error) {
	var ou models.OrganizationUser
	err := r.db.GetDB().NewSelect().
		Model(&ou).
		Where("user_id = ?", userID).
		Order("created_at ASC").
		Limit(1).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", model.ErrorUserNotFound
		}
		return "", err
	}
	return ou.OrganizationID, nil
}

const tokenTypePAT = "pat"

func (r *DbUserRepository) FindByToken(ctx context.Context, token string) (model.User, error) {
	var t models.UserToken
	err := r.db.GetDB().NewSelect().Model(&t).Where("token = ?", token).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, model.ErrInvalidToken
		}
		return model.User{}, err
	}
	if t.ExpiresAt != nil && time.Now().After(t.ExpiresAt.Time()) {
		return model.User{}, model.ErrInvalidToken
	}
	return r.GetUser(ctx, t.UserID)
}

func (r *DbUserRepository) CreateToken(ctx context.Context, userID string) (string, error) {
	secret, err := util.GenerateSecureToken(16) // 32 hex chars to match char(32)
	if err != nil {
		return "", err
	}
	row := &models.UserToken{
		TokenID:   util.GenerateID(),
		Token:     secret,
		TokenType: tokenTypePAT,
		UserID:    userID,
		CreatedAt: util.NewDateTime(),
	}
	if _, err := r.db.GetDB().NewInsert().Model(row).Exec(ctx); err != nil {
		return "", err
	}
	return secret, nil
}

func (r *DbUserRepository) ListTokens(ctx context.Context, userID string) ([]model.Token, error) {
	var rows []models.UserToken
	err := r.db.GetDB().NewSelect().Model(&rows).Where("user_id = ?", userID).Order("created_at DESC").Scan(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.Token, 0, len(rows))
	for i := range rows {
		t := model.Token{ID: rows[i].TokenID, Type: rows[i].TokenType, CreatedAt: rows[i].CreatedAt.Time()}
		if rows[i].ExpiresAt != nil {
			e := rows[i].ExpiresAt.Time()
			t.ExpiresAt = &e
		}
		out = append(out, t)
	}
	return out, nil
}

func (r *DbUserRepository) RevokeToken(ctx context.Context, userID, tokenID string) error {
	_, err := r.db.GetDB().NewDelete().
		Model((*models.UserToken)(nil)).
		Where("token_id = ? AND user_id = ?", tokenID, userID).
		Exec(ctx)
	return err
}
