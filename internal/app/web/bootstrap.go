package web

import (
	"net/http"
	"os"
	"path/filepath"
	"time"
	"winterflow/internal/app"
	auth2 "winterflow/internal/app/web/auth"
	corsmw "winterflow/internal/app/web/middleware/cors"
	logmw "winterflow/internal/app/web/middleware/logger"
	"winterflow/internal/infra/bootstrap"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-pkgz/auth/v2"
	"github.com/go-pkgz/auth/v2/avatar"
	"github.com/go-pkgz/auth/v2/token"
)

type Routing struct {
	Logger  *logger.Logger
	Cfg     *config.Config
	Factory *bootstrap.Factory
	Router  *chi.Mux
	Auth    *auth.Service
}

func NewServer(mode app.AppMode, log *logger.Logger, cfg *config.Config) *http.Server {
	ro := Routing{
		Logger:  log,
		Cfg:     cfg,
		Factory: bootstrap.NewFactory(mode),
		Router:  chi.NewRouter(),
	}
	ro.registerMiddleware()
	ro.registerAuth()
	ro.registerRoutes()

	return &http.Server{Addr: ":" + cfg.GetApiPort(), Handler: ro.Router}
}

func (ro *Routing) registerMiddleware() {
	//ro.Router.Use(middleware.Logger)
	ro.Router.Use(logmw.WithLogger(ro.Logger))
	ro.Router.Use(middleware.RequestID)
	ro.Router.Use(middleware.RealIP)
	ro.Router.Use(corsmw.UseCORS(ro.Cfg.GetAllowedOrigins()))
	//ro.Router.Use(middleware.Timeout(ro.Cfg.GetRouteTimeout() * time.Second))
	ro.Router.Use(middleware.Recoverer)
}

func (ro *Routing) registerAuth() {
	avaDir := filepath.Join(os.TempDir(), "winterflow_avatars")
	options := auth.Opts{
		SecretReader: token.SecretFunc(func(id string) (string, error) { // secret key for JWT
			return ro.Cfg.GetJwtSecret(), nil
		}),
		TokenDuration:   time.Minute * 5,
		CookieDuration:  time.Hour * 24,
		Issuer:          "winterflow",
		URL:             ro.Cfg.GetWebURL(),
		AvatarStore:     avatar.NewLocalFS(avaDir),
		AvatarRoutePath: "/avatar",
		DisableXSRF:     true,
		ClaimsUpd: token.ClaimsUpdFunc(func(claims token.Claims) token.Claims {
			ro.Logger.Debug("updating claims: %+v", claims)
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
	ggle := auth2.NewGoogleAuth(*ro.Cfg)
	if ggle.IsEnabled() {
		ro.Logger.Debug("Enabling Google Auth")
		service.AddCustomProvider(ggle.Name, ggle.Client, ggle.Options)
	}

	ro.Auth = service
}
