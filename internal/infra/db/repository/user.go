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
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
	"winterflow/pkg/pat"
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
		CreatedAt:      types.NewDateTime(),
	}

	user := &models.User{
		UserID:    util.GenerateID(),
		Name:      dto.Name,
		Avatar:    util.RefString(dto.AvatarURL),
		CreatedAt: types.NewDateTime(),
		LastSeen:  types.NewDateTime(),
	}

	orgUser := &models.OrganizationUser{
		BaseModel:      bun.BaseModel{},
		OrganizationID: org.OrganizationID,
		UserID:         user.UserID,
		Role:           model.RoleOwner.Value(),
		CreatedAt:      types.NewDateTime(),
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

// FindOrCreateUser resolves the user for a connected account, creating the
// user (plus their org and membership) on first login.
func (r *DbUserRepository) FindOrCreateUser(ctx context.Context, d dto.UserDTO) (model.User, error) {
	user, err := r.GetByConnectedAccount(ctx, d.Provider, d.AccountID)
	if err != nil {
		if !errors.Is(err, model.ErrorUserNotFound) {
			return model.User{}, err
		}
		cu, err := r.CreateUser(ctx, d)
		if err != nil {
			r.log.Error("failed to create user: %v", err)
			return model.User{}, err
		}
		return cu, nil
	}
	return user, nil
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

// lastUsedWriteInterval throttles last_used_at updates so a busy API client
// costs at most one write per minute, not one per request.
const lastUsedWriteInterval = time.Minute

func (r *DbUserRepository) FindByToken(ctx context.Context, plaintext string) (model.User, error) {
	var t models.UserToken
	err := r.db.GetDB().NewSelect().Model(&t).
		Where("token_hash = ?", pat.Hash(plaintext)).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, model.ErrInvalidToken
		}
		return model.User{}, err
	}
	if t.ExpiresAt != nil && time.Now().After(t.ExpiresAt.Time()) {
		return model.User{}, model.ErrInvalidToken
	}
	if t.LastUsedAt == nil || time.Since(t.LastUsedAt.Time()) > lastUsedWriteInterval {
		now := types.NewDateTime()
		if _, err := r.db.GetDB().NewUpdate().Model((*models.UserToken)(nil)).
			Set("last_used_at = ?", now).
			Where("token_id = ?", t.TokenID).
			Exec(ctx); err != nil {
			r.log.Error("FindByToken: stamp last_used_at", "error", err)
		}
	}
	return r.GetUser(ctx, t.UserID)
}

func (r *DbUserRepository) CreateToken(ctx context.Context, userID, name string, expiresAt *time.Time) (model.UserToken, string, error) {
	plaintext, hash, prefix, err := pat.Generate()
	if err != nil {
		return model.UserToken{}, "", err
	}
	row := &models.UserToken{
		TokenID:     util.GenerateID(),
		UserID:      userID,
		Name:        name,
		TokenPrefix: prefix,
		TokenHash:   hash,
		TokenType:   "pat",
		CreatedAt:   types.NewDateTime(),
	}
	if expiresAt != nil {
		dt := types.DateTime(*expiresAt)
		row.ExpiresAt = &dt
	}
	if _, err := r.db.GetDB().NewInsert().Model(row).Exec(ctx); err != nil {
		return model.UserToken{}, "", err
	}
	return toDomainToken(row), plaintext, nil
}

func (r *DbUserRepository) ListTokens(ctx context.Context, userID string) ([]model.UserToken, error) {
	var rows []models.UserToken
	if err := r.db.GetDB().NewSelect().Model(&rows).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]model.UserToken, 0, len(rows))
	for i := range rows {
		out = append(out, toDomainToken(&rows[i]))
	}
	return out, nil
}

func (r *DbUserRepository) DeleteToken(ctx context.Context, userID, tokenID string) error {
	res, err := r.db.GetDB().NewDelete().Model((*models.UserToken)(nil)).
		Where("token_id = ? AND user_id = ?", tokenID, userID).
		Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.ErrTokenNotFound
	}
	return nil
}

func toDomainToken(t *models.UserToken) model.UserToken {
	out := model.UserToken{
		ID:        t.TokenID,
		UserID:    t.UserID,
		Name:      t.Name,
		Prefix:    t.TokenPrefix,
		CreatedAt: t.CreatedAt.Time(),
	}
	if t.ExpiresAt != nil {
		x := t.ExpiresAt.Time()
		out.ExpiresAt = &x
	}
	if t.LastUsedAt != nil {
		x := t.LastUsedAt.Time()
		out.LastUsedAt = &x
	}
	return out
}
