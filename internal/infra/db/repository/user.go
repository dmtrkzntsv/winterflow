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
	"golang.org/x/crypto/bcrypt"
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

// --- local credentials & org membership -------------------------------------

// normalizeEmail is applied at every boundary so lookups and uniqueness are
// case/whitespace-insensitive.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(h), err
}

func (r *DbUserRepository) CountUsers(ctx context.Context) (int, error) {
	return r.db.GetDB().NewSelect().Model((*models.User)(nil)).Count(ctx)
}

// BootstrapLocalAdmin creates the very first account of an instance from the
// first login's email+password: user + personal org (owner) + local connected
// account + credentials. Refuses when any user already exists — the count is
// re-checked inside the transaction so a racing double-submit has one winner.
func (r *DbUserRepository) BootstrapLocalAdmin(ctx context.Context, name, email, password string) (model.User, error) {
	email = normalizeEmail(email)
	hash, err := hashPassword(password)
	if err != nil {
		return model.User{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = email
		if i := strings.IndexByte(email, '@'); i > 0 {
			name = email[:i]
		}
	}

	org := &models.Organization{
		OrganizationID: util.GenerateID(),
		Name:           name + "'s org",
		CreatedAt:      types.NewDateTime(),
	}
	user := &models.User{
		UserID:    util.GenerateID(),
		Name:      name,
		CreatedAt: types.NewDateTime(),
		LastSeen:  types.NewDateTime(),
	}

	err = r.db.Transaction(ctx, func(tx bun.IDB) error {
		n, err := tx.NewSelect().Model((*models.User)(nil)).Count(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return model.ErrNotBootstrap
		}
		if _, err := tx.NewInsert().Model(org).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(user).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&models.OrganizationUser{
			OrganizationID: org.OrganizationID,
			UserID:         user.UserID,
			Role:           model.RoleOwner.Value(),
			CreatedAt:      types.NewDateTime(),
		}).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&models.UserConnectedAccount{
			Provider:   "local",
			ExternalID: email,
			UserID:     user.UserID,
		}).Exec(ctx); err != nil {
			return err
		}
		_, err = tx.NewInsert().Model(&models.UserCredentials{
			UserID:       user.UserID,
			Email:        email,
			PasswordHash: hash,
			UpdatedAt:    types.NewDateTime(),
		}).Exec(ctx)
		return err
	})
	if err != nil {
		return model.User{}, err
	}
	return model.User{ID: user.UserID, Name: user.Name, CreatedAt: user.CreatedAt.Time(), LastSeenAt: user.LastSeen.Time()}, nil
}

func (r *DbUserRepository) VerifyLocalCredentials(ctx context.Context, email, password string) (model.User, error) {
	var creds models.UserCredentials
	err := r.db.GetDB().NewSelect().Model(&creds).
		Where("email = ?", normalizeEmail(email)).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, model.ErrInvalidCredentials
		}
		return model.User{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(creds.PasswordHash), []byte(password)) != nil {
		return model.User{}, model.ErrInvalidCredentials
	}
	return r.GetUser(ctx, creds.UserID)
}

// CreateMemberUser creates an admin-provisioned account inside an existing
// organization: user + credentials (must-change temp password) + local
// connected account + membership with the given role. No personal org.
func (r *DbUserRepository) CreateMemberUser(ctx context.Context, orgID, name, email, role, tempPassword string) (model.User, error) {
	email = normalizeEmail(email)
	hash, err := hashPassword(tempPassword)
	if err != nil {
		return model.User{}, err
	}
	user := &models.User{
		UserID:    util.GenerateID(),
		Name:      name,
		CreatedAt: types.NewDateTime(),
		LastSeen:  types.NewDateTime(),
	}
	err = r.db.Transaction(ctx, func(tx bun.IDB) error {
		taken, err := tx.NewSelect().Model((*models.UserCredentials)(nil)).
			Where("email = ?", email).Exists(ctx)
		if err != nil {
			return err
		}
		if taken {
			return model.ErrEmailTaken
		}
		if _, err := tx.NewInsert().Model(user).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&models.UserCredentials{
			UserID:             user.UserID,
			Email:              email,
			PasswordHash:       hash,
			MustChangePassword: true,
			UpdatedAt:          types.NewDateTime(),
		}).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&models.UserConnectedAccount{
			Provider:   "local",
			ExternalID: email,
			UserID:     user.UserID,
		}).Exec(ctx); err != nil {
			return err
		}
		_, err = tx.NewInsert().Model(&models.OrganizationUser{
			OrganizationID: orgID,
			UserID:         user.UserID,
			Role:           role,
			CreatedAt:      types.NewDateTime(),
		}).Exec(ctx)
		return err
	})
	if err != nil {
		return model.User{}, err
	}
	return model.User{ID: user.UserID, Name: user.Name, CreatedAt: user.CreatedAt.Time(), LastSeenAt: user.LastSeen.Time()}, nil
}

func (r *DbUserRepository) SetPassword(ctx context.Context, userID, password string, mustChange bool) error {
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	res, err := r.db.GetDB().NewUpdate().Model((*models.UserCredentials)(nil)).
		Set("password_hash = ?", hash).
		Set("must_change_password = ?", mustChange).
		Set("updated_at = ?", types.NewDateTime()).
		Where("user_id = ?", userID).
		Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.ErrorUserNotFound
	}
	return nil
}

func (r *DbUserRepository) GetCredentials(ctx context.Context, userID string) (model.Credentials, error) {
	var creds models.UserCredentials
	err := r.db.GetDB().NewSelect().Model(&creds).
		Where("user_id = ?", userID).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Credentials{}, model.ErrorUserNotFound
		}
		return model.Credentials{}, err
	}
	return model.Credentials{Email: creds.Email, MustChangePassword: creds.MustChangePassword}, nil
}

func (r *DbUserRepository) ListMembers(ctx context.Context, orgID string) ([]model.Member, error) {
	var rows []models.OrganizationUser
	if err := r.db.GetDB().NewSelect().Model(&rows).
		Relation("User").
		Where("organization_user.organization_id = ?", orgID).
		Order("organization_user.created_at ASC").
		Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]model.Member, 0, len(rows))
	for _, row := range rows {
		m := model.Member{Role: row.Role}
		if row.User != nil {
			m.User = model.User{
				ID:         row.User.UserID,
				Name:       row.User.Name,
				AvatarURL:  util.DerefString(row.User.Avatar),
				CreatedAt:  row.User.CreatedAt.Time(),
				LastSeenAt: row.User.LastSeen.Time(),
			}
		}
		if creds, err := r.GetCredentials(ctx, row.UserID); err == nil {
			m.Email = creds.Email
			m.Provider = "local"
		} else {
			var acc models.UserConnectedAccount
			if err := r.db.GetDB().NewSelect().Model(&acc).
				Where("user_id = ?", row.UserID).Limit(1).Scan(ctx); err == nil {
				m.Provider = acc.Provider
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// ownerCount reports how many owners the org has (for last-owner guards).
func ownerCount(ctx context.Context, tx bun.IDB, orgID string) (int, error) {
	return tx.NewSelect().Model((*models.OrganizationUser)(nil)).
		Where("organization_id = ? AND role = ?", orgID, model.RoleOwner.Value()).
		Count(ctx)
}

func (r *DbUserRepository) memberRole(ctx context.Context, tx bun.IDB, orgID, userID string) (string, error) {
	var row models.OrganizationUser
	err := tx.NewSelect().Model(&row).
		Where("organization_id = ? AND user_id = ?", orgID, userID).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", model.ErrorUserNotFound
		}
		return "", err
	}
	return row.Role, nil
}

func (r *DbUserRepository) UpdateMemberRole(ctx context.Context, orgID, userID, role string) error {
	return r.db.Transaction(ctx, func(tx bun.IDB) error {
		current, err := r.memberRole(ctx, tx, orgID, userID)
		if err != nil {
			return err
		}
		if current == model.RoleOwner.Value() && role != model.RoleOwner.Value() {
			n, err := ownerCount(ctx, tx, orgID)
			if err != nil {
				return err
			}
			if n <= 1 {
				return model.ErrLastOwner
			}
		}
		_, err = tx.NewUpdate().Model((*models.OrganizationUser)(nil)).
			Set("role = ?", role).
			Where("organization_id = ? AND user_id = ?", orgID, userID).
			Exec(ctx)
		return err
	})
}

// RemoveMember deletes the member's user row outright (memberships,
// credentials, connected accounts, and PATs go with it via FK cascade).
// Safe because every non-owner member is an admin-provisioned account.
func (r *DbUserRepository) RemoveMember(ctx context.Context, orgID, userID string) error {
	return r.db.Transaction(ctx, func(tx bun.IDB) error {
		current, err := r.memberRole(ctx, tx, orgID, userID)
		if err != nil {
			return err
		}
		if current == model.RoleOwner.Value() {
			n, err := ownerCount(ctx, tx, orgID)
			if err != nil {
				return err
			}
			if n <= 1 {
				return model.ErrLastOwner
			}
		}
		_, err = tx.NewDelete().Model((*models.User)(nil)).
			Where("user_id = ?", userID).
			Exec(ctx)
		return err
	})
}

// RoleOf returns the caller's role in their primary organization.
func (r *DbUserRepository) RoleOf(ctx context.Context, userID string) (string, error) {
	var row models.OrganizationUser
	err := r.db.GetDB().NewSelect().Model(&row).
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
	return row.Role, nil
}

// RegisterLocalUser is open self-signup (distributed topology): user + their
// OWN organization (owner) + credentials + local connected account. The
// standalone claim step uses BootstrapLocalAdmin instead.
func (r *DbUserRepository) RegisterLocalUser(ctx context.Context, name, email, password string) (model.User, error) {
	email = normalizeEmail(email)
	hash, err := hashPassword(password)
	if err != nil {
		return model.User{}, err
	}
	org := &models.Organization{
		OrganizationID: util.GenerateID(),
		Name:           name + "'s org",
		CreatedAt:      types.NewDateTime(),
	}
	user := &models.User{
		UserID:    util.GenerateID(),
		Name:      name,
		CreatedAt: types.NewDateTime(),
		LastSeen:  types.NewDateTime(),
	}
	err = r.db.Transaction(ctx, func(tx bun.IDB) error {
		taken, err := tx.NewSelect().Model((*models.UserCredentials)(nil)).
			Where("email = ?", email).Exists(ctx)
		if err != nil {
			return err
		}
		if taken {
			return model.ErrEmailTaken
		}
		if _, err := tx.NewInsert().Model(org).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(user).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&models.OrganizationUser{
			OrganizationID: org.OrganizationID,
			UserID:         user.UserID,
			Role:           model.RoleOwner.Value(),
			CreatedAt:      types.NewDateTime(),
		}).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&models.UserConnectedAccount{
			Provider:   "local",
			ExternalID: email,
			UserID:     user.UserID,
		}).Exec(ctx); err != nil {
			return err
		}
		_, err = tx.NewInsert().Model(&models.UserCredentials{
			UserID:       user.UserID,
			Email:        email,
			PasswordHash: hash,
			UpdatedAt:    types.NewDateTime(),
		}).Exec(ctx)
		return err
	})
	if err != nil {
		return model.User{}, err
	}
	return model.User{ID: user.UserID, Name: user.Name, CreatedAt: user.CreatedAt.Time(), LastSeenAt: user.LastSeen.Time()}, nil
}

func (r *DbUserRepository) GetOrganization(ctx context.Context, orgID string) (model.Organization, error) {
	var org models.Organization
	err := r.db.GetDB().NewSelect().Model(&org).
		Where("organization_id = ?", orgID).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Organization{}, model.ErrorUserNotFound
		}
		return model.Organization{}, err
	}
	return model.Organization{ID: org.OrganizationID, Name: org.Name, Icon: org.Icon, Color: org.Color}, nil
}

func (r *DbUserRepository) UpdateOrganization(ctx context.Context, orgID, name, icon, color string) error {
	res, err := r.db.GetDB().NewUpdate().Model((*models.Organization)(nil)).
		Set("name = ?", name).
		Set("icon = ?", icon).
		Set("color = ?", color).
		Where("organization_id = ?", orgID).
		Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.ErrorUserNotFound
	}
	return nil
}
