package web

import (
	"net/http"
	"os"
	"path/filepath"
	"time"
	auth2 "winterflow/internal/app/web/auth"
	corsmw "winterflow/internal/app/web/middleware/cors"
	logmw "winterflow/internal/app/web/middleware/logger"
	"winterflow/internal/domain/port"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-pkgz/auth/v2"
	"github.com/go-pkgz/auth/v2/avatar"
	"github.com/go-pkgz/auth/v2/token"
)

type Server struct {
	Logger  *logger.Logger
	Cfg     *config.Config
	Factory port.AppFactory
	Router  *chi.Mux
	Auth    *auth.Service
}

func NewServer(log *logger.Logger, cfg *config.Config, factory port.AppFactory) *http.Server {
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
	//s.Router.Use(middleware.Timeout(s.Cfg.GetRouteTimeout() * time.Second))
	s.Router.Use(middleware.Recoverer)
}

func (s *Server) registerAuth() {
	avaDir := filepath.Join(os.TempDir(), "winterflow_avatars")
	options := auth.Opts{
		SecretReader: token.SecretFunc(func(id string) (string, error) { // secret key for JWT
			return s.Cfg.GetJwtSecret(), nil
		}),
		TokenDuration:   time.Minute * 5,
		CookieDuration:  time.Hour * 24,
		Issuer:          "winterflow",
		URL:             s.Cfg.GetWebURL(),
		AvatarStore:     avatar.NewLocalFS(avaDir),
		AvatarRoutePath: "/avatar",
		DisableXSRF:     true,
		ClaimsUpd: token.ClaimsUpdFunc(func(claims token.Claims) token.Claims {
			s.Logger.Debug("updating claims: %+v", claims)
			if claims.User != nil {
				if claims.User.Email == "" {
					if e := claims.User.StrAttr("email"); e != "" {
						claims.User.Email = e
					}
				}
			}
			return claims
		}),
	}

	service := auth.NewService(options)
	ggle := auth2.NewGoogleAuth(*s.Cfg)
	if ggle.IsEnabled() {
		s.Logger.Debug("Enabling Google Auth")
		service.AddCustomProvider(ggle.Name, ggle.Client, ggle.Options)
	}

	s.Auth = service
}
