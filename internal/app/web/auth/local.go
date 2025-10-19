package auth

import (
	"winterflow/pkg/config"

	"github.com/go-pkgz/auth/v2"
	"github.com/go-pkgz/auth/v2/provider"
)

const LocalProvider = "local"

func AddLocalAuth(service *auth.Service, config config.Config) {
	if !config.IsAuthSupported(LocalProvider) {
		return
	}

	login, pass := config.GetLocalAuth()
	service.AddDirectProvider(LocalProvider, provider.CredCheckerFunc(func(user, password string) (ok bool, err error) {
		if user == login && password == pass {
			ok = true
		} else {
			ok = false
		}
		return ok, nil
	}))
}
