package auth

import (
	"crypto/sha1"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"github.com/go-pkgz/auth/v2"
	"github.com/go-pkgz/auth/v2/provider"
	"github.com/go-pkgz/auth/v2/token"
	"golang.org/x/oauth2/google"
)

const GoogleProvider = "google"

func AddGoogleAuth(service *auth.Service, log *logger.Logger, config config.Config) {
	if !config.IsAuthSupported(GoogleProvider) {
		return
	}

	gcid, gcs := config.GetGoogleAuth()
	service.AddCustomProvider(GoogleProvider, auth.Client{Cid: gcid, Csecret: gcs}, provider.CustomHandlerOpt{
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
	})
	log.Debug("Enabling Google Auth")
}
