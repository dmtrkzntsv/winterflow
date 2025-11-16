package auth

import (
	"errors"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"github.com/go-pkgz/auth/v2"
	"github.com/go-pkgz/auth/v2/provider"
)

const EnvProvider = ".env"

func AddEnvAuth(service *auth.Service, log *logger.Logger, config config.Config) {
	if !config.IsAuthSupported(EnvProvider) {
		return
	}

	username, password := config.GetEnvAuth()
	service.AddDirectProvider(EnvProvider, provider.CredCheckerFunc(func(usr, psw string) (ok bool, err error) {
		if usr == username && psw == password {
			return true, nil
		}
		ok = false
		return false, errors.New("invalid login or password")
	}))
	log.Debug("Enabling .env auth provider")
}
