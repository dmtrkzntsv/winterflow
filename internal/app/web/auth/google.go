package auth

import (
	"crypto/sha1"
	"winterflow/pkg/config"

	"github.com/go-pkgz/auth/v2"
	"github.com/go-pkgz/auth/v2/provider"
	"github.com/go-pkgz/auth/v2/token"
	"golang.org/x/oauth2/google"
)

func NewGoogleAuth(config config.Config) CustomAuthProvider {
	if !config.IsAuthSupported("google") {
		return CustomAuthProvider{
			Enabled: false,
		}
	}

	gcid, gcs := config.GetGoogleAuth()
	return CustomAuthProvider{
		Enabled: true,
		Name:    "google",
		Client:  auth.Client{Cid: gcid, Csecret: gcs},
		Options: provider.CustomHandlerOpt{
			Endpoint: google.Endpoint,
			InfoURL:  "https://www.googleapis.com/oauth2/v3/userinfo",
			Scopes: []string{
				"https://www.googleapis.com/auth/userinfo.profile",
				"https://www.googleapis.com/auth/userinfo.email",
			},
			MapUserFn: func(data provider.UserData, _ []byte) token.User {
				u := token.User{
					ID:      "google_" + token.HashID(sha1.New(), data.Value("sub")),
					Name:    data.Value("name"),
					Picture: data.Value("picture"),
					Email:   data.Value("email"),
				}
				return u
			},
		},
	}
}
