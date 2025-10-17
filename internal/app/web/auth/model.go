package auth

import (
	"github.com/go-pkgz/auth/v2"
	"github.com/go-pkgz/auth/v2/provider"
)

type CustomAuthProvider struct {
	Enabled bool
	Name    string
	Client  auth.Client
	Options provider.CustomHandlerOpt
}

func (cp CustomAuthProvider) IsEnabled() bool {
	return cp.Enabled
}
