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
	CountUsers(ctx context.Context) (int, error)
	BootstrapLocalAdmin(ctx context.Context, email, password string) (model.User, error)
	VerifyLocalCredentials(ctx context.Context, email, password string) (model.User, error)
}

// localCredChecker returns the credential check: on a fresh instance (zero
// users) the first login CREATES the admin account with the submitted
// email+password; afterwards it verifies against stored bcrypt hashes. The
// zero-users condition is strict — anything looser would let a stranger mint
// an admin on an existing instance.
func localCredChecker(users LocalUserStore) func(user, password string) (bool, error) {
	return func(email, password string) (bool, error) {
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" || password == "" {
			return false, nil
		}
		ctx := context.Background()

		n, err := users.CountUsers(ctx)
		if err != nil {
			return false, err
		}
		if n == 0 {
			if _, err := users.BootstrapLocalAdmin(ctx, email, password); err != nil {
				if errors.Is(err, model.ErrNotBootstrap) {
					// Lost a bootstrap race — fall through to normal verify.
					_, verr := users.VerifyLocalCredentials(ctx, email, password)
					return verr == nil, nil
				}
				return false, err
			}
			return true, nil
		}

		if _, err := users.VerifyLocalCredentials(ctx, email, password); err != nil {
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
// connected account written at bootstrap / member creation.
func AddLocalAuth(service *auth.Service, log *logger.Logger, users LocalUserStore) {
	check := localCredChecker(users)
	service.AddDirectProvider(LocalProvider, provider.CredCheckerFunc(func(usr, psw string) (bool, error) {
		return check(usr, psw)
	}))
	log.Debug("enabled local email+password auth provider")
}
