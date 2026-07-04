package auth

import (
	"context"
	"errors"
	"strings"
	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"

	"github.com/go-pkgz/auth/v2"
	"github.com/go-pkgz/auth/v2/provider"
)

// LocalProvider is the always-on email+password provider. It cannot be
// disabled: local credentials are WinterFlow's default identity.
const LocalProvider = "local"

// LocalUserStore is the slice of port.UserService the local provider needs.
type LocalUserStore interface {
	VerifyLocalCredentials(ctx context.Context, email, password string) (model.User, error)
}

// localCredChecker verifies an email+password pair against stored bcrypt
// hashes. Login is verify-only: accounts are created exclusively through
// POST /api/v1/auth/register (or by an admin at /org/members).
func localCredChecker(users LocalUserStore) func(user, password string) (bool, error) {
	return func(email, password string) (bool, error) {
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" || password == "" {
			return false, nil
		}
		if _, err := users.VerifyLocalCredentials(context.Background(), email, password); err != nil {
			if errors.Is(err, model.ErrInvalidCredentials) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
}

// AddLocalAuth registers the local email+password direct provider. The login
// endpoint is /auth/local/login?user=<email>&passwd=<password> (go-pkgz
// direct-provider shape); ClaimsUpd resolves the user through the "local"
// connected account written at registration / member creation.
func AddLocalAuth(service *auth.Service, log *logger.Logger, users LocalUserStore) {
	check := localCredChecker(users)
	service.AddDirectProvider(LocalProvider, provider.CredCheckerFunc(func(usr, psw string) (bool, error) {
		return check(usr, psw)
	}))
	log.Debug("enabled local email+password auth provider")
}
