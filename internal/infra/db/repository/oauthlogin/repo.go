// Package oauthlogin implements port.SignInRepository: the provider
// accounts linked to users (user_connected_accounts, leaving out the
// email+password accounts' "local" rows) and the in-flight provider
// sign-ins (oauth_login_states).
package oauthlogin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/util"
)

type (
	identityRow       = sqlitegen.UserConnectedAccount
	stateRow          = sqlitegen.OauthLoginState
	subjectParams     = sqlitegen.GetIdentityByProviderSubjectParams
	userProvParams    = sqlitegen.GetIdentityByUserProviderParams
	emailParams       = sqlitegen.UpdateIdentityEmailParams
	rekeyParams       = sqlitegen.RekeyIdentityParams
	insertIDParams    = sqlitegen.InsertIdentityIfAbsentParams
	deleteIDParams    = sqlitegen.DeleteIdentityByUserProviderParams
	insertStateParams = sqlitegen.InsertOAuthLoginStateParams
)

type querier interface {
	GetIdentityByProviderSubject(ctx context.Context, db backend.DBTX, p subjectParams) (identityRow, error)
	GetIdentityByUserProvider(ctx context.Context, db backend.DBTX, p userProvParams) (identityRow, error)
	ListIdentitiesByUser(ctx context.Context, db backend.DBTX, userID string) ([]identityRow, error)
	CountIdentitiesByUser(ctx context.Context, db backend.DBTX, userID string) (int64, error)
	UpdateIdentityEmail(ctx context.Context, db backend.DBTX, p emailParams) (int64, error)
	RekeyIdentity(ctx context.Context, db backend.DBTX, p rekeyParams) (int64, error)
	InsertIdentityIfAbsent(ctx context.Context, db backend.DBTX, p insertIDParams) (int64, error)
	DeleteIdentityByUserProvider(ctx context.Context, db backend.DBTX, p deleteIDParams) (int64, error)
	LockUserIdentities(ctx context.Context, db backend.DBTX, userID string) (string, error)
	UserHasPassword(ctx context.Context, db backend.DBTX, userID string) (bool, error)
	SignInEmailInUse(ctx context.Context, db backend.DBTX, address string) (bool, error)
	InsertOAuthLoginState(ctx context.Context, db backend.DBTX, p insertStateParams) error
	ConsumeOAuthLoginState(ctx context.Context, db backend.DBTX, stateHash string) (stateRow, error)
	DeleteExpiredOAuthLoginStates(ctx context.Context, db backend.DBTX, before types.DateTime) (int64, error)
}

// localProvider marks the email+password accounts' own rows, which are not
// linked identities.
const localProvider = "local"

// Repo is the sign-in repository.
type Repo struct {
	tx *backend.TxManager
	q  querier
}

var _ port.SignInRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}}, nil
	}
	return nil, fmt.Errorf("sign-in repository: unknown database driver %q", driver)
}

func hydrate(row identityRow) model.Identity {
	return model.Identity{
		Provider: row.Provider, Subject: row.ExternalID, UserID: row.UserID,
		Email: util.DerefString(row.Email), CreatedAt: row.CreatedAt.Time(),
	}
}

func notFound(err, as error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return as
	}
	return err
}

// IdentityBySubject finds a linked provider account.
func (r *Repo) IdentityBySubject(ctx context.Context, provider, subject string) (model.Identity, error) {
	if provider == localProvider {
		return model.Identity{}, model.ErrIdentityNotFound
	}
	row, err := r.q.GetIdentityByProviderSubject(ctx, r.tx.Querier(ctx), subjectParams{Provider: provider, ExternalID: subject})
	if err != nil {
		return model.Identity{}, notFound(err, model.ErrIdentityNotFound)
	}
	return hydrate(row), nil
}

// Identities lists the user's linked provider accounts, oldest first.
func (r *Repo) Identities(ctx context.Context, userID string) ([]model.Identity, error) {
	rows, err := r.q.ListIdentitiesByUser(ctx, r.tx.Querier(ctx), userID)
	if err != nil {
		return nil, err
	}
	out := make([]model.Identity, len(rows))
	for i, row := range rows {
		out[i] = hydrate(row)
	}
	return out, nil
}

// UpdateIdentityEmail records the address a provider vouched for.
func (r *Repo) UpdateIdentityEmail(ctx context.Context, provider, subject, email string) error {
	_, err := r.q.UpdateIdentityEmail(ctx, r.tx.Querier(ctx), emailParams{Email: util.RefString(email), Provider: provider, ExternalID: subject})
	return err
}

// RekeyIdentity moves a legacy row to the provider's subject.
func (r *Repo) RekeyIdentity(ctx context.Context, provider, legacyID, subject string) (bool, error) {
	n, err := r.q.RekeyIdentity(ctx, r.tx.Querier(ctx), rekeyParams{Subject: subject, Provider: provider, LegacyID: legacyID})
	return n > 0, err
}

// lockUser takes the user's row lock for the rest of the transaction;
// model.ErrorUserNotFound when the user is gone.
func (r *Repo) lockUser(ctx context.Context, userID string) error {
	_, err := r.q.LockUserIdentities(ctx, r.tx.Querier(ctx), userID)
	return notFound(err, model.ErrorUserNotFound)
}

// LinkIdentity attaches a provider account to id.UserID.
func (r *Repo) LinkIdentity(ctx context.Context, id model.Identity) error {
	if id.Provider == localProvider {
		return fmt.Errorf("sign-in repository: %q is not a provider", localProvider)
	}
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := r.lockUser(ctx, id.UserID); err != nil {
			return err
		}
		db := r.tx.Querier(ctx)
		existing, err := r.q.GetIdentityByProviderSubject(ctx, db, subjectParams{Provider: id.Provider, ExternalID: id.Subject})
		switch {
		case err == nil && existing.UserID != id.UserID:
			return model.ErrIdentityTaken
		case err == nil:
			_, err = r.q.UpdateIdentityEmail(ctx, db, emailParams{Email: util.RefString(id.Email), Provider: id.Provider, ExternalID: id.Subject})
			return err
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if _, err := r.q.GetIdentityByUserProvider(ctx, db, userProvParams{UserID: id.UserID, Provider: id.Provider}); err == nil {
			return model.ErrProviderAlreadyLinked
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		n, err := r.q.InsertIdentityIfAbsent(ctx, db, insertIDParams{
			Provider: id.Provider, ExternalID: id.Subject, UserID: id.UserID,
			Email: util.RefString(id.Email), CreatedAt: types.DateTime(id.CreatedAt),
		})
		if err != nil {
			return err
		}
		if n == 0 {
			// Linked to someone else between the read and the insert.
			return model.ErrIdentityTaken
		}
		return nil
	})
}

// UnlinkIdentity removes the user's account of provider unless it is the
// only way into a passwordless account. The user's row lock serializes it
// with every other link and unlink of that user, so two unlinks cannot both
// count two identities and both delete.
func (r *Repo) UnlinkIdentity(ctx context.Context, userID, provider string) error {
	if provider == localProvider {
		return model.ErrIdentityNotFound
	}
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := r.lockUser(ctx, userID); err != nil {
			return err
		}
		db := r.tx.Querier(ctx)
		if _, err := r.q.GetIdentityByUserProvider(ctx, db, userProvParams{UserID: userID, Provider: provider}); err != nil {
			return notFound(err, model.ErrIdentityNotFound)
		}
		hasPassword, err := r.q.UserHasPassword(ctx, db, userID)
		if err != nil {
			return err
		}
		if !hasPassword {
			n, err := r.q.CountIdentitiesByUser(ctx, db, userID)
			if err != nil {
				return err
			}
			if n <= 1 {
				return model.ErrLastSignInMethod
			}
		}
		_, err = r.q.DeleteIdentityByUserProvider(ctx, db, deleteIDParams{UserID: userID, Provider: provider})
		return err
	})
}

// EmailInUse reports whether an account answers to email already.
func (r *Repo) EmailInUse(ctx context.Context, email string) (bool, error) {
	return r.q.SignInEmailInUse(ctx, r.tx.Querier(ctx), email)
}

// InsertLoginState stores an in-flight sign-in.
func (r *Repo) InsertLoginState(ctx context.Context, s model.LoginState) error {
	return r.q.InsertOAuthLoginState(ctx, r.tx.Querier(ctx), insertStateParams{
		StateHash: s.StateHash, Provider: s.Provider, Nonce: s.Nonce, CodeVerifier: s.CodeVerifier,
		FlowHash: s.FlowHash, Intent: s.Intent, ReturnTo: s.ReturnTo, LinkUserID: util.RefString(s.LinkUserID),
		CreatedAt: types.DateTime(s.CreatedAt), ExpiresAt: types.DateTime(s.ExpiresAt),
	})
}

// ConsumeLoginState deletes and returns a state.
func (r *Repo) ConsumeLoginState(ctx context.Context, stateHash string) (model.LoginState, error) {
	row, err := r.q.ConsumeOAuthLoginState(ctx, r.tx.Querier(ctx), stateHash)
	if err != nil {
		return model.LoginState{}, notFound(err, model.ErrLoginStateNotFound)
	}
	return model.LoginState{
		StateHash: row.StateHash, Provider: row.Provider, Nonce: row.Nonce, CodeVerifier: row.CodeVerifier,
		FlowHash: row.FlowHash, Intent: row.Intent, ReturnTo: row.ReturnTo, LinkUserID: util.DerefString(row.LinkUserID),
		CreatedAt: row.CreatedAt.Time(), ExpiresAt: row.ExpiresAt.Time(),
	}, nil
}

// DeleteExpiredLoginStates drops the states that expired before now.
func (r *Repo) DeleteExpiredLoginStates(ctx context.Context, now time.Time) error {
	_, err := r.q.DeleteExpiredOAuthLoginStates(ctx, r.tx.Querier(ctx), types.DateTime(now))
	return err
}
