package web

import (
	"context"
	"net"
	"net/http"
	corsmw "winterflow/internal/app/web/middleware/cors"
	logmw "winterflow/internal/app/web/middleware/logger"
	"winterflow/internal/app/web/middleware/realip"
	"winterflow/internal/domain/dto"
	"winterflow/internal/infra/bootstrap"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	Logger *logger.Logger
	Cfg    *config.ServerConfig
	Deps   *bootstrap.Deps
	Router *chi.Mux
}

// NewServer builds the HTTP server. baseCtx is the process lifecycle context
// (canceled on shutdown); it becomes the BaseContext for every request so that
// long-lived handlers — notably the SSE stream — observe shutdown via
// r.Context().Done() and unblock srv.Shutdown instead of holding it open until
// the timeout.
func NewServer(baseCtx context.Context, deps *bootstrap.Deps) *http.Server {
	cfg := deps.Cfg
	s := Server{
		Logger: deps.Log,
		Cfg:    cfg,
		Deps:   deps,
		Router: chi.NewRouter(),
	}
	s.registerMiddleware()
	s.registerRoutes()

	return &http.Server{
		Addr:        cfg.GetApiListenAddr(),
		Handler:     s.Router,
		BaseContext: func(net.Listener) context.Context { return baseCtx },
	}
}

func (s *Server) registerMiddleware() {
	//s.Router.Use(middleware.Logger)
	s.Router.Use(logmw.WithLogger(s.Logger))
	s.Router.Use(middleware.RequestID)
	s.Router.Use(realip.Middleware)
	s.Router.Use(corsmw.UseCORS(s.Cfg.GetAllowedOrigins()))
	s.Router.Use(middleware.Recoverer)
}

// autoClaimStandaloneServer links the embedded agent's pending registration to
// the user's organization, so a standalone user never has to type the pairing
// code. It is a no-op when there is no single pending registration (already
// claimed, or — defensively — more than one), making it safe to run on every
// login (and registration).
func autoClaimStandaloneServer(ctx context.Context, d *bootstrap.Deps, userID string) {
	code, ok, err := d.ServerRepository.PendingRegistrationCode(ctx)
	if err != nil {
		d.Log.Error("auto-claim: failed to read pending registration", "error", err)
		return
	}
	if !ok {
		return
	}

	orgID, err := d.UserService.PrimaryOrganizationID(ctx, userID)
	if err != nil {
		d.Log.Error("auto-claim: failed to resolve organization", "error", err)
		return
	}

	srv, err := d.ServerRepository.ClaimServer(ctx, dto.ClaimServerDTO{Code: code, OrganizationID: orgID})
	if err != nil {
		d.Log.Error("auto-claim: failed to claim server", "error", err)
		return
	}
	d.Log.Info("auto-claimed standalone server", "server_id", srv.ID, "organization_id", orgID)
}

// registrationOpen mirrors the register endpoint's policy, for sign-ins that
// may create an account (sign-in providers): a fresh instance (zero users)
// is always claimable; afterwards standalone is closed and distributed
// follows REGISTRATION_ENABLED. firstOnly marks an opening that exists only
// because the instance is fresh (the claim step), so the account creation
// re-checks that no user exists by then.
func registrationOpen(ctx context.Context, d *bootstrap.Deps) (open, firstOnly bool) {
	n, err := d.UserService.CountUsers(ctx)
	if err != nil {
		d.Log.Error("registrationOpen: count users", "error", err)
		return false, false
	}
	selfSignup := !d.Cfg.IsStandalone() && d.Cfg.IsRegistrationEnabled()
	if n == 0 {
		return true, !selfSignup
	}
	return selfSignup, false
}
