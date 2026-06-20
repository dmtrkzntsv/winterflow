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
	"winterflow/internal/infra/bootstrap"
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
	Logger *logger.Logger
	Cfg    *config.ServerConfig
	Deps   *bootstrap.Deps
	Router *chi.Mux
	Auth   *auth.Service
}

func NewServer(deps *bootstrap.Deps) *http.Server {
	cfg := deps.Cfg
	s := Server{
		Logger: deps.Log,
		Cfg:    cfg,
		Deps:   deps,
		Router: chi.NewRouter(),
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
				user, err := s.Deps.UserService.FindOrCreateUser(ctx, dto.UserDTO{
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

				// Standalone: the embedded agent self-registered at boot with a
				// pairing code but isn't linked to any org. Claim it into the
				// first user's org automatically so the server "just appears".
				if s.Cfg.IsStandalone() {
					s.autoClaimStandaloneServer(ctx, user.ID)
				}
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

// autoClaimStandaloneServer links the embedded agent's pending registration to
// the user's organization, so a standalone user never has to type the pairing
// code. It is a no-op when there is no single pending registration (already
// claimed, or — defensively — more than one), making it safe to run on every
// login.
func (s *Server) autoClaimStandaloneServer(ctx context.Context, userID string) {
	code, ok, err := s.Deps.ServerService.PendingRegistrationCode(ctx)
	if err != nil {
		s.Logger.Error("auto-claim: failed to read pending registration", "error", err)
		return
	}
	if !ok {
		return
	}

	orgID, err := s.Deps.UserService.PrimaryOrganizationID(ctx, userID)
	if err != nil {
		s.Logger.Error("auto-claim: failed to resolve organization", "error", err)
		return
	}

	srv, err := s.Deps.ServerService.ClaimServer(ctx, dto.ClaimServerDTO{Code: code, OrganizationID: orgID})
	if err != nil {
		s.Logger.Error("auto-claim: failed to claim server", "error", err)
		return
	}
	s.Logger.Info("auto-claimed standalone server", "server_id", srv.ID, "organization_id", orgID)
}
