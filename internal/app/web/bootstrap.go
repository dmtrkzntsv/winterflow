package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	authprvd "winterflow/internal/app/web/auth"
	corsmw "winterflow/internal/app/web/middleware/cors"
	logmw "winterflow/internal/app/web/middleware/logger"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/port"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-pkgz/auth/v2"
	"github.com/go-pkgz/auth/v2/avatar"
	logger2 "github.com/go-pkgz/auth/v2/logger"
	"github.com/go-pkgz/auth/v2/token"
)

type Server struct {
	Logger  *logger.Logger
	Cfg     *config.ServerConfig
	Factory port.AppFactory
	Router  *chi.Mux
	Auth    *auth.Service
}

func NewServer(log *logger.Logger, cfg *config.ServerConfig, factory port.AppFactory) *http.Server {
	s := Server{
		Logger:  log,
		Cfg:     cfg,
		Factory: factory,
		Router:  chi.NewRouter(),
	}
	s.registerMiddleware()
	s.registerAuth()
	s.registerRoutes()

	return &http.Server{Addr: ":" + cfg.GetApiPort(), Handler: s.Router}
}

func (s *Server) registerMiddleware() {
	//s.Router.Use(middleware.Logger)
	s.Router.Use(logmw.WithLogger(s.Logger))
	s.Router.Use(middleware.RequestID)
	s.Router.Use(middleware.RealIP)
	s.Router.Use(corsmw.UseCORS(s.Cfg.GetAllowedOrigins()))
	s.Router.Use(middleware.Recoverer)
}

func (s *Server) registerAuth() {
	options := auth.Opts{
		SecretReader: token.SecretFunc(func(aud string) (string, error) {
			return s.Cfg.GetJwtSecret(), nil
		}),
		Logger: logger2.Func(func(format string, args ...interface{}) {
			s.Logger.Debug(format, args...)
		}),
		SecureCookies:   true,
		TokenDuration:   time.Minute * 5,
		CookieDuration:  time.Hour * 24,
		Issuer:          "winterflow",
		URL:             s.Cfg.GetWebURL(),
		AvatarStore:     avatar.NewLocalFS(s.Cfg.GetAvatarsStoragePath()),
		AvatarRoutePath: "/avatar",
		DisableXSRF:     true,
		ClaimsUpd: token.ClaimsUpdFunc(func(claims token.Claims) token.Claims {
			s.Logger.Debug("updating claims: %+v", claims)
			if claims.User != nil {
				ctx := context.Background()
				parts := strings.SplitN(claims.User.ID, "_", 2)
				provider := parts[0]
				accountId := parts[1]
				if provider == authprvd.EnvProvider {
					accountId = authprvd.EnvProvider
				}
				user, err := s.Factory.NewUserService().FindOrCreateUser(ctx, dto.UserDTO{
					Provider:  provider,
					AccountID: accountId,
					UserID:    "",
					Name:      claims.User.Name,
					AvatarURL: strings.TrimPrefix(claims.User.Picture, s.Cfg.GetWebURL()),
				})
				if err != nil {
					s.Logger.Error("failed to find or create user: %v", err)
					return claims
				}
				claims.ID = user.ID
				claims.User.ID = user.ID
				claims.User.Picture = user.AvatarURL
				claims.User.Name = user.Name
				claims.User.SetStrAttr("provider", provider)
			}
			return claims
		}),
		BasicAuthChecker: func(userID, pat string) (bool, token.User, error) {
			// Validate the provided token string
			// Authorization: Basic base64(userID:PAT)
			// @todo implement PAT check
			//valid, userInfo := checkPAT(userID, token)
			//if valid {
			//    return true, userInfo, nil
			//}
			return false, token.User{}, errors.New("invalid token")
		},
	}

	service := auth.NewService(options)
	authprvd.AddGoogleAuth(service, s.Logger, *s.Cfg)
	authprvd.AddEnvAuth(service, s.Logger, *s.Cfg)

	s.Auth = service
}
