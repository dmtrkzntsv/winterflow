// Package user implements port.UserRepository: users, their organizations
// and memberships, connected accounts and local credentials.
package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/auth/password"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

type (
	userRow           = sqlitegen.User
	credentialsRow    = sqlitegen.UserCredential
	organizationRow   = sqlitegen.Organization
	insertUserParams  = sqlitegen.InsertUserParams
	accountParams     = sqlitegen.InsertConnectedAccountParams
	accountKeyParams  = sqlitegen.GetConnectedAccountUserIDParams
	identityParams    = sqlitegen.InsertIdentityParams
	credentialsParams = sqlitegen.InsertCredentialsParams
	passwordParams    = sqlitegen.UpdatePasswordParams
	rehashParams      = sqlitegen.RehashPasswordParams
	takenByOther      = sqlitegen.EmailTakenByOtherParams
	identityEmail     = sqlitegen.UserHasIdentityEmailParams
	insertOrgParams   = sqlitegen.InsertOrganizationParams
	updateOrgParams   = sqlitegen.UpdateOrganizationParams
	membershipParams  = sqlitegen.InsertOrganizationUserParams
	memberKeyParams   = sqlitegen.LockOrganizationMemberParams
	isMemberParams    = sqlitegen.IsOrganizationMemberParams
)

type querier interface {
	// users.sql
	GetUser(ctx context.Context, db backend.DBTX, userID string) (userRow, error)
	CountUsers(ctx context.Context, db backend.DBTX) (int64, error)
	InsertUser(ctx context.Context, db backend.DBTX, p insertUserParams) error
	DeleteUser(ctx context.Context, db backend.DBTX, userID string) error
	GetConnectedAccountUserID(ctx context.Context, db backend.DBTX, p accountKeyParams) (string, error)
	InsertConnectedAccount(ctx context.Context, db backend.DBTX, p accountParams) error
	InsertIdentity(ctx context.Context, db backend.DBTX, p identityParams) error
	GetFirstConnectedAccountProvider(ctx context.Context, db backend.DBTX, userID string) (string, error)
	InsertCredentials(ctx context.Context, db backend.DBTX, p credentialsParams) error
	GetCredentialsByEmail(ctx context.Context, db backend.DBTX, email string) (credentialsRow, error)
	GetCredentialsByUser(ctx context.Context, db backend.DBTX, userID string) (credentialsRow, error)
	EmailTaken(ctx context.Context, db backend.DBTX, email string) (bool, error)
	EmailTakenByOther(ctx context.Context, db backend.DBTX, p takenByOther) (bool, error)
	UserHasIdentityEmail(ctx context.Context, db backend.DBTX, p identityEmail) (bool, error)
	LockUserCreation(ctx context.Context, db backend.DBTX) error
	UpdatePassword(ctx context.Context, db backend.DBTX, p passwordParams) (int64, error)
	RehashPassword(ctx context.Context, db backend.DBTX, p rehashParams) (int64, error)
	// organizations.sql
	InsertOrganization(ctx context.Context, db backend.DBTX, p insertOrgParams) error
	GetOrganization(ctx context.Context, db backend.DBTX, organizationID string) (organizationRow, error)
	UpdateOrganization(ctx context.Context, db backend.DBTX, p updateOrgParams) (int64, error)
	InsertOrganizationUser(ctx context.Context, db backend.DBTX, p membershipParams) error
	GetPrimaryOrganizationID(ctx context.Context, db backend.DBTX, userID string) (string, error)
	LockOrganizationMember(ctx context.Context, db backend.DBTX, p memberKeyParams) (string, error)
	IsOrganizationMember(ctx context.Context, db backend.DBTX, p isMemberParams) (bool, error)
	ListOrganizationMembers(ctx context.Context, db backend.DBTX, organizationID string) ([]userRow, error)
}

// Repo is the users repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.UserRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("user repository: unknown database driver %q", driver)
}

func hydrate(row userRow) model.User {
	return model.User{
		ID:         row.UserID,
		Name:       row.Name,
		AvatarURL:  util.DerefString(row.Avatar),
		CreatedAt:  row.CreatedAt.Time(),
		LastSeenAt: row.LastSeen.Time(),
	}
}

// GetByConnectedAccount resolves a provider account to its user;
// model.ErrorUserNotFound when unknown.
func (r *Repo) GetByConnectedAccount(ctx context.Context, provider, accountID string) (model.User, error) {
	userID, err := r.q.GetConnectedAccountUserID(ctx, r.tx.Querier(ctx), accountKeyParams{Provider: provider, ExternalID: accountID})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, model.ErrorUserNotFound
		}
		return model.User{}, err
	}
	return r.GetUser(ctx, userID)
}

// CreateUser creates a user, their own organization (named after the
// lower-cased user name; they own it) and the connected account, in one
// transaction.
func (r *Repo) CreateUser(ctx context.Context, d dto.UserDTO) (model.User, error) {
	now := types.NewDateTime()
	orgID := util.GenerateID()
	user := insertUserParams{
		UserID:    util.GenerateID(),
		Name:      d.Name,
		Avatar:    util.RefString(d.AvatarURL),
		CreatedAt: now,
		LastSeen:  now,
	}
	err := r.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := r.beginCreate(ctx, ""); err != nil {
			return err
		}
		db := r.tx.Querier(ctx)
		if err := r.q.InsertOrganization(ctx, db, insertOrgParams{OrganizationID: orgID, Name: strings.ToLower(d.Name) + "'s org", CreatedAt: now}); err != nil {
			return err
		}
		if err := r.q.InsertUser(ctx, db, user); err != nil {
			return err
		}
		if err := r.q.InsertOrganizationUser(ctx, db, membershipParams{OrganizationID: orgID, UserID: user.UserID, Role: model.RoleOwner.Value(), CreatedAt: now}); err != nil {
			return err
		}
		return r.q.InsertConnectedAccount(ctx, db, accountParams{Provider: d.Provider, ExternalID: d.AccountID, UserID: user.UserID})
	})
	if err != nil {
		return model.User{}, err
	}
	return model.User{
		ID:         user.UserID,
		Name:       user.Name,
		AvatarURL:  util.DerefString(user.Avatar),
		CreatedAt:  now.Time(),
		LastSeenAt: now.Time(),
	}, nil
}

// FindOrCreateUser resolves the user for a connected account, creating the
// user (plus their org and membership) on first login.
func (r *Repo) FindOrCreateUser(ctx context.Context, d dto.UserDTO) (model.User, error) {
	user, err := r.GetByConnectedAccount(ctx, d.Provider, d.AccountID)
	if err != nil {
		if !errors.Is(err, model.ErrorUserNotFound) {
			return model.User{}, err
		}
		cu, err := r.CreateUser(ctx, d)
		if err != nil {
			r.log.Error("failed to create user", "error", err)
			return model.User{}, err
		}
		return cu, nil
	}
	return user, nil
}

// GetUser returns a user; model.ErrorUserNotFound when there is none.
func (r *Repo) GetUser(ctx context.Context, userID string) (model.User, error) {
	row, err := r.q.GetUser(ctx, r.tx.Querier(ctx), userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, model.ErrorUserNotFound
		}
		return model.User{}, err
	}
	return hydrate(row), nil
}

// PrimaryOrganizationID returns the user's organization. The 1-user-per-org
// model gives each user exactly one (created at signup); if a user ever
// belongs to several, the oldest membership wins so the result is stable.
func (r *Repo) PrimaryOrganizationID(ctx context.Context, userID string) (string, error) {
	id, err := r.q.GetPrimaryOrganizationID(ctx, r.tx.Querier(ctx), userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", model.ErrorUserNotFound
		}
		return "", err
	}
	return id, nil
}

// --- local credentials & org membership -------------------------------------

// localProvider is the connected-account provider of email+password accounts.
const localProvider = "local"

// normalizeEmail is applied at every boundary so lookups and uniqueness are
// case/whitespace-insensitive.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// CountUsers reports how many users exist.
func (r *Repo) CountUsers(ctx context.Context) (int, error) {
	n, err := r.q.CountUsers(ctx, r.tx.Querier(ctx))
	return int(n), err
}

// beginCreate opens every user-creating transaction: it takes the
// instance-wide user-creation lock (a transaction-scoped advisory lock on
// Postgres; SQLite serializes transactions already), so "the first user
// becomes the owner" and "an email answers to one account" are decided
// against every account committed before this one, never against a
// snapshot a concurrent creator is about to change. When email is not empty
// it is refused with model.ErrEmailTaken while any account answers to it: a
// password account's email or a provider identity's (case-insensitive).
func (r *Repo) beginCreate(ctx context.Context, email string) error {
	db := r.tx.Querier(ctx)
	if err := r.q.LockUserCreation(ctx, db); err != nil {
		return fmt.Errorf("lock user creation: %w", err)
	}
	if email == "" {
		return nil
	}
	taken, err := r.q.EmailTaken(ctx, db, normalizeEmail(email))
	if err != nil {
		return err
	}
	if taken {
		return model.ErrEmailTaken
	}
	return nil
}

// localAccount inserts the rows every local (email + password) account has:
// user, credentials, connected account "local"/email, membership.
func (r *Repo) localAccount(ctx context.Context, orgID, role string, user insertUserParams, email, hash string, mustChange bool) error {
	db := r.tx.Querier(ctx)
	if err := r.q.InsertUser(ctx, db, user); err != nil {
		return err
	}
	if err := r.q.InsertOrganizationUser(ctx, db, membershipParams{OrganizationID: orgID, UserID: user.UserID, Role: role, CreatedAt: types.NewDateTime()}); err != nil {
		return err
	}
	if err := r.q.InsertConnectedAccount(ctx, db, accountParams{Provider: localProvider, ExternalID: email, UserID: user.UserID}); err != nil {
		return err
	}
	return r.q.InsertCredentials(ctx, db, credentialsParams{
		UserID: user.UserID, Email: email, PasswordHash: hash, MustChangePassword: mustChange, UpdatedAt: types.NewDateTime(),
	})
}

func newUser(name string) insertUserParams {
	now := types.NewDateTime()
	return insertUserParams{UserID: util.GenerateID(), Name: name, CreatedAt: now, LastSeen: now}
}

func created(u insertUserParams) model.User {
	return model.User{ID: u.UserID, Name: u.Name, CreatedAt: u.CreatedAt.Time(), LastSeenAt: u.LastSeen.Time()}
}

// BootstrapLocalAdmin creates the very first account of an instance from the
// first login's email+password: user + personal org (owner) + local connected
// account + credentials. Refuses when any user already exists: the count is
// re-checked inside the transaction, under the user-creation lock, so a
// racing double-submit (or a racing first provider sign-in) has one winner.
func (r *Repo) BootstrapLocalAdmin(ctx context.Context, name, email, pw string) (model.User, error) {
	email = normalizeEmail(email)
	hash, err := password.Hash(ctx, pw)
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
	orgID := util.GenerateID()
	user := newUser(name)
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := r.beginCreate(ctx, ""); err != nil {
			return err
		}
		n, err := r.q.CountUsers(ctx, r.tx.Querier(ctx))
		if err != nil {
			return err
		}
		if n > 0 {
			return model.ErrNotBootstrap
		}
		if err := r.q.InsertOrganization(ctx, r.tx.Querier(ctx), insertOrgParams{OrganizationID: orgID, Name: name + "'s org", CreatedAt: types.NewDateTime()}); err != nil {
			return err
		}
		return r.localAccount(ctx, orgID, model.RoleOwner.Value(), user, email, hash, false)
	})
	if err != nil {
		return model.User{}, err
	}
	return created(user), nil
}

// VerifyLocalCredentials checks an email+password pair;
// model.ErrInvalidCredentials on any mismatch.
func (r *Repo) VerifyLocalCredentials(ctx context.Context, email, pw string) (model.User, error) {
	creds, err := r.q.GetCredentialsByEmail(ctx, r.tx.Querier(ctx), normalizeEmail(email))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Spend what a real verify costs, so an unknown email answers
			// as slowly as a wrong password (no account-existence oracle).
			if err := password.VerifyDummy(ctx, pw); err != nil {
				return model.User{}, err
			}
			return model.User{}, model.ErrInvalidCredentials
		}
		return model.User{}, err
	}
	ok, needsRehash, err := password.Verify(ctx, creds.PasswordHash, pw)
	if err != nil {
		return model.User{}, err
	}
	if !ok {
		return model.User{}, model.ErrInvalidCredentials
	}
	if needsRehash {
		r.rehash(ctx, creds.UserID, pw, creds.PasswordHash)
	}
	return r.GetUser(ctx, creds.UserID)
}

// rehash upgrades a verified legacy hash to the current scheme, only while
// oldHash is still the stored one: a password changed or reset since the
// verify is never overwritten with the old password. A failure is logged and
// never fails the login: the old hash still works next time.
func (r *Repo) rehash(ctx context.Context, userID, pw, oldHash string) {
	hash, err := password.Hash(ctx, pw)
	if err == nil {
		_, err = r.q.RehashPassword(ctx, r.tx.Querier(ctx), rehashParams{
			PasswordHash: hash, UpdatedAt: types.NewDateTime(), UserID: userID, OldHash: oldHash,
		})
	}
	if err != nil {
		r.log.Error("rehash password", "error", err)
	}
}

// CreateMemberUser creates an account inside an existing organization:
// user + credentials (must-change temp password) + local connected account +
// membership. No personal org. The stored role is model.RoleAdmin.
func (r *Repo) CreateMemberUser(ctx context.Context, orgID, name, email, tempPassword string) (model.User, error) {
	email = normalizeEmail(email)
	hash, err := password.Hash(ctx, tempPassword)
	if err != nil {
		return model.User{}, err
	}
	user := newUser(name)
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := r.beginCreate(ctx, email); err != nil {
			return err
		}
		return r.localAccount(ctx, orgID, model.RoleAdmin.Value(), user, email, hash, true)
	})
	if err != nil {
		return model.User{}, err
	}
	return created(user), nil
}

// SetPassword replaces the hash and must-change flag;
// model.ErrorUserNotFound for a user without local credentials.
func (r *Repo) SetPassword(ctx context.Context, userID, pw string, mustChange bool) error {
	hash, err := password.Hash(ctx, pw)
	if err != nil {
		return err
	}
	n, err := r.q.UpdatePassword(ctx, r.tx.Querier(ctx), passwordParams{
		PasswordHash: hash, MustChangePassword: mustChange, UpdatedAt: types.NewDateTime(), UserID: userID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return model.ErrorUserNotFound
	}
	return nil
}

// AddPassword creates the local credentials (and the "local" connected
// account) of an account that has none, under the user-creation lock so
// the email-in-use rule holds against a concurrent registration.
func (r *Repo) AddPassword(ctx context.Context, userID, email, pw string) error {
	email = normalizeEmail(email)
	hash, err := password.Hash(ctx, pw)
	if err != nil {
		return err
	}
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		db := r.tx.Querier(ctx)
		if err := r.q.LockUserCreation(ctx, db); err != nil {
			return fmt.Errorf("lock user creation: %w", err)
		}
		if _, err := r.q.GetCredentialsByUser(ctx, db, userID); err == nil {
			return model.ErrPasswordAlreadySet
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		own, err := r.q.UserHasIdentityEmail(ctx, db, identityEmail{UserID: userID, Email: email})
		if err != nil {
			return err
		}
		if !own {
			return model.ErrNotIdentityEmail
		}
		taken, err := r.q.EmailTakenByOther(ctx, db, takenByOther{Email: email, UserID: userID})
		if err != nil {
			return err
		}
		if taken {
			return model.ErrEmailTaken
		}
		if err := r.q.InsertConnectedAccount(ctx, db, accountParams{Provider: localProvider, ExternalID: email, UserID: userID}); err != nil {
			return err
		}
		return r.q.InsertCredentials(ctx, db, credentialsParams{
			UserID: userID, Email: email, PasswordHash: hash, UpdatedAt: types.NewDateTime(),
		})
	})
}

// GetCredentials returns the non-secret credential info;
// model.ErrorUserNotFound for users without local credentials.
func (r *Repo) GetCredentials(ctx context.Context, userID string) (model.Credentials, error) {
	creds, err := r.q.GetCredentialsByUser(ctx, r.tx.Querier(ctx), userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Credentials{}, model.ErrorUserNotFound
		}
		return model.Credentials{}, err
	}
	return model.Credentials{Email: creds.Email, MustChangePassword: creds.MustChangePassword}, nil
}

// ListMembers returns the org's members, oldest membership first, each with
// their email and "local" when they have credentials, else the provider of
// their first connected account (ordered by provider, then external_id, so
// the choice is deterministic). A member with neither is listed with an
// empty email and provider; any other lookup failure is returned.
func (r *Repo) ListMembers(ctx context.Context, orgID string) ([]model.Member, error) {
	db := r.tx.Querier(ctx)
	rows, err := r.q.ListOrganizationMembers(ctx, db, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]model.Member, 0, len(rows))
	for _, row := range rows {
		m := model.Member{User: hydrate(row)}
		creds, err := r.q.GetCredentialsByUser(ctx, db, row.UserID)
		switch {
		case err == nil:
			m.Email = creds.Email
			m.Provider = localProvider
		case errors.Is(err, sql.ErrNoRows):
			provider, err := r.q.GetFirstConnectedAccountProvider(ctx, db, row.UserID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			m.Provider = provider
		default:
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// RemoveMember deletes the member's user row outright (memberships,
// credentials, connected accounts and access tokens go with it via FK
// cascade).
// Only a member of orgID can be removed: anyone else is
// model.ErrorUserNotFound and stays untouched.
//
// callerID is the member doing the removing. Their own membership row is
// re-read inside the transaction (locked FOR UPDATE on Postgres; SQLite
// serializes writers), so two members removing each other concurrently
// cannot empty the org: the second removal finds its caller gone and is
// refused with model.ErrorUserNotFound.
func (r *Repo) RemoveMember(ctx context.Context, orgID, callerID, userID string) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		db := r.tx.Querier(ctx)
		if _, err := r.q.LockOrganizationMember(ctx, db, memberKeyParams{OrganizationID: orgID, UserID: callerID}); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return model.ErrorUserNotFound
			}
			return err
		}
		member, err := r.q.IsOrganizationMember(ctx, db, isMemberParams{OrganizationID: orgID, UserID: userID})
		if err != nil {
			return err
		}
		if !member {
			return model.ErrorUserNotFound
		}
		return r.q.DeleteUser(ctx, db, userID)
	})
}

// RegisterLocalUser is open self-signup (distributed topology): user + their
// OWN organization (owner) + credentials + local connected account.
func (r *Repo) RegisterLocalUser(ctx context.Context, name, email, pw string) (model.User, error) {
	email = normalizeEmail(email)
	hash, err := password.Hash(ctx, pw)
	if err != nil {
		return model.User{}, err
	}
	orgID := util.GenerateID()
	user := newUser(name)
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := r.beginCreate(ctx, email); err != nil {
			return err
		}
		if err := r.q.InsertOrganization(ctx, r.tx.Querier(ctx), insertOrgParams{OrganizationID: orgID, Name: name + "'s org", CreatedAt: types.NewDateTime()}); err != nil {
			return err
		}
		return r.localAccount(ctx, orgID, model.RoleOwner.Value(), user, email, hash, false)
	})
	if err != nil {
		return model.User{}, err
	}
	return created(user), nil
}

// CreateProviderUser is a sign-in provider's registration: a passwordless
// user named name, their own organization (owner) and the linked identity
// id (its UserID is filled in), in one transaction. firstOnly makes it the
// instance's claim step instead: refused with model.ErrNotBootstrap unless
// there are no users yet, re-checked inside the transaction under the
// user-creation lock so two racing first sign-ins have one winner. An
// identity email any account already answers to is model.ErrEmailTaken.
func (r *Repo) CreateProviderUser(ctx context.Context, name string, id model.Identity, firstOnly bool) (model.User, error) {
	orgID := util.GenerateID()
	user := newUser(name)
	err := r.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := r.beginCreate(ctx, id.Email); err != nil {
			return err
		}
		db := r.tx.Querier(ctx)
		if firstOnly {
			n, err := r.q.CountUsers(ctx, db)
			if err != nil {
				return err
			}
			if n > 0 {
				return model.ErrNotBootstrap
			}
		}
		now := types.NewDateTime()
		if err := r.q.InsertOrganization(ctx, db, insertOrgParams{OrganizationID: orgID, Name: name + "'s org", CreatedAt: now}); err != nil {
			return err
		}
		if err := r.q.InsertUser(ctx, db, user); err != nil {
			return err
		}
		if err := r.q.InsertOrganizationUser(ctx, db, membershipParams{OrganizationID: orgID, UserID: user.UserID, Role: model.RoleOwner.Value(), CreatedAt: now}); err != nil {
			return err
		}
		return r.q.InsertIdentity(ctx, db, identityParams{
			Provider: id.Provider, ExternalID: id.Subject, UserID: user.UserID,
			Email: util.RefString(id.Email), CreatedAt: types.DateTime(id.CreatedAt), Issuer: util.RefString(id.Issuer),
		})
	})
	if err != nil {
		return model.User{}, err
	}
	return created(user), nil
}

// GetOrganization returns the org's identity; model.ErrorUserNotFound when
// unknown.
func (r *Repo) GetOrganization(ctx context.Context, orgID string) (model.Organization, error) {
	org, err := r.q.GetOrganization(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Organization{}, model.ErrorUserNotFound
		}
		return model.Organization{}, err
	}
	return model.Organization{ID: org.OrganizationID, Name: org.Name, Icon: org.Icon, Color: org.Color}, nil
}

// UpdateOrganization sets the org's identity; model.ErrorUserNotFound when
// unknown.
func (r *Repo) UpdateOrganization(ctx context.Context, orgID, name, icon, color string) error {
	n, err := r.q.UpdateOrganization(ctx, r.tx.Querier(ctx), updateOrgParams{Name: name, Icon: icon, Color: color, OrganizationID: orgID})
	if err != nil {
		return err
	}
	if n == 0 {
		return model.ErrorUserNotFound
	}
	return nil
}
