package auth

import (
	"errors"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"github.com/go-pkgz/auth/v2"
	"github.com/go-pkgz/auth/v2/provider"
)

const LocalProvider = "local"

func AddLocalAuth(service *auth.Service, log *logger.Logger, config config.Config) {
	if !config.IsAuthSupported(LocalProvider) {
		return
	}

	login, pass := config.GetLocalAuth()
	service.AddDirectProvider(LocalProvider, provider.CredCheckerFunc(func(user, password string) (ok bool, err error) {
		if user == login && password == pass {
			return true, nil
		}
		ok = false
		return false, errors.New("invalid login or password")
	}))
	log.Debug("Enabling Local Auth")
}
