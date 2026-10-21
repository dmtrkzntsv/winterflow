package web

import (
	"net/http"
	"slices"
	"winterflow/internal/app/web/spa"
	"winterflow/internal/domain/port"
	webdist "winterflow/web"

	"winterflow/internal/app/web/api"
	authprvd "winterflow/internal/app/web/auth"
	halerts "winterflow/internal/app/web/handler/alerts"
	happ "winterflow/internal/app/web/handler/app"
	hauth "winterflow/internal/app/web/handler/auth"
	hdocker "winterflow/internal/app/web/handler/docker"
	henroll "winterflow/internal/app/web/handler/enroll"
	"winterflow/internal/app/web/handler/monitoring"
	"winterflow/internal/app/web/handler/notification"
	horg "winterflow/internal/app/web/handler/org"
	hrollouts "winterflow/internal/app/web/handler/rollouts"
	"winterflow/internal/app/web/handler/server"
	hsharedvars "winterflow/internal/app/web/handler/sharedvars"
	htemplates "winterflow/internal/app/web/handler/templates"
	huser "winterflow/internal/app/web/handler/user"
	"winterflow/internal/app/web/middleware/patauth"
	"winterflow/internal/app/web/middleware/ratelimit"
	"winterflow/internal/app/web/util"
	svuc "winterflow/internal/domain/usecase/sharedvars"
	"winterflow/internal/infra/bootstrap"
)

func (s *Server) registerRoutes() {
	// Auth endpoints are the API's only bcrypt-priced requests — throttle them
	// per client IP so credential floods answer 429 instead of burning CPU.
	// One limiter is shared by the go-pkgz /auth mount and /api/v1/auth/register.
	authLimiter := ratelimit.New(5, 10)

	authRoutes, avaRoutes := s.Auth.Handlers()
	s.Router.With(authLimiter.Middleware).Mount("/auth", authRoutes) // add auth handlers
	s.Router.Mount("/avatar", avaRoutes)                             // add avatar handler

	amw := s.Auth.Middleware()
	// authMW = PAT bearer first, JWT session otherwise. Every User route
	// uses this so tokens work everywhere a browser session does.
	authMW := patauth.Middleware(s.Deps.UserService, amw.Auth)
	// No role guard: every org member may do everything; server-addressed
	// handlers still check the caller's org owns the server.
	enrollLimiter := ratelimit.New(5, 30)
	// Starting an enrollment stores a row: about 3 a minute per IP, burst 5.
	enrollStartLimiter := ratelimit.New(0.05, 5)
	guards := api.Guards{AuthLimit: authLimiter.Middleware, EnrollLimit: enrollLimiter.Middleware,
		EnrollStartLimit: enrollStartLimiter.Middleware}
	routes := routeTable(s.Deps, guards)
	if s.Deps.Enrollment == nil {
		// Standalone: agents do not enroll by code; the routes answer 404.
		routes = slices.DeleteFunc(routes, func(rt api.Route) bool { return rt.Tag == "enroll" })
	}
	api.Register(s.Router, routes, authMW)

	// Serve the embedded SPA for everything the API doesn't claim (history-mode
	// fallback). A source build without `make web-build` embeds only the
	// committed placeholder; the server then stays API-only.
	if distFS, ok := webdist.DistFS(); ok {
		s.Router.NotFound(spa.Handler(distFS).ServeHTTP)
		s.Logger.Info("serving embedded web UI")
	} else {
		s.Logger.Info("no embedded web UI build; serving API only (run `make web-build` before building the binary)")
	}
}

// APIRoutes is the full route table for documentation (cmd/openapi): the
// handlers are built over zero dependencies and the guards are nil, so it
// must never be passed to api.Register.
func APIRoutes() []api.Route {
	return routeTable(&bootstrap.Deps{}, api.Guards{})
}

// routeTable collects every handler package's routes, plus the health probe
// and the go-pkgz mounts (documented only). The NotificationManager is shared
// across handlers: the SSE stream subscribes to it and the usecases publish
// to it, so every handler MUST receive the same instance from d.
func routeTable(d *bootstrap.Deps, g api.Guards) []api.Route {
	authAPI := hauth.NewHandler(&hauth.Deps{Logger: d.Log, Cfg: d.Cfg, Users: d.UserService})
	notificationAPI := notification.NewHandler(&notification.Deps{
		Logger:              d.Log,
		NotificationManager: d.NotificationManager,
	})
	appsAPI := happ.NewHandler(&happ.Deps{
		Logger:                  d.Log,
		CommandDispatcher:       d.CommandDispatcher,
		AppRepository:           d.AppRepository,
		AppDomainRepository:     d.AppDomainRepository,
		ServerDomainRepository:  d.ServerDomainRepository,
		ServerStorageRepository: d.ServerStorageRepository,
		StatusCache:             d.StatusCache,
		Servers:                 d.ServerRepository,
		Instances:               d.TemplateRepository,
	})
	dockerAPI := hdocker.NewHandler(&hdocker.Deps{
		Logger:            d.Log,
		CommandDispatcher: d.CommandDispatcher,
		Servers:           d.ServerRepository,
	})
	// Typed nils must not become non-nil interfaces: standalone (and the
	// spec-only table) has no enrollment usecase.
	var (
		claimer     server.Claimer
		revoker     port.Revoker
		enrollments henroll.Enrollments
	)
	if d.Enrollment != nil {
		claimer, enrollments = d.Enrollment, d.Enrollment
	}
	if d.Revoker != nil {
		revoker = d.Revoker
	}
	enrollAPI := henroll.NewHandler(&henroll.Deps{Logger: d.Log, Enrollments: enrollments})
	serversAPI := server.NewHandler(&server.Deps{
		Logger:           d.Log,
		ServerRepository: d.ServerRepository,
		UserService:      d.UserService,
		StatusCache:      d.StatusCache,
		LatestMetrics:    d.LatestMetrics,
		Revoker:          revoker,
		Cfg:              d.Cfg,
		Enrollment:       claimer,
	})
	monitoringAPI := monitoring.NewHandler(&monitoring.Deps{
		Logger:      d.Log,
		Servers:     d.ServerRepository,
		Metrics:     d.MetricRepository,
		Latest:      d.LatestMetrics,
		StatusCache: d.StatusCache,
		Disabled:    !d.Cfg.IsMetricsEnabled(),
		Alerts:      d.AlertRepository,
	})
	// Typed nils must not become non-nil interfaces: without JWT_SECRET
	// there is no box, and the spec-only route table has no notifier.
	var box halerts.Sealer
	if d.ChannelBox != nil {
		box = d.ChannelBox
	}
	var tester halerts.Tester
	if d.AlertNotifier != nil {
		tester = d.AlertNotifier
	}
	alertsAPI := halerts.NewHandler(&halerts.Deps{
		Logger:  d.Log,
		Users:   d.UserService,
		Servers: d.ServerRepository,
		Apps:    d.AppRepository,
		Alerts:  d.AlertRepository,
		Box:     box,
		Tester:  tester,
	})
	usersAPI := huser.NewHandler(&huser.Deps{Logger: d.Log, Tokens: d.UserService, Users: d.UserService})
	orgAPI := horg.NewHandler(&horg.Deps{Logger: d.Log, Users: d.UserService})
	// A nil rollout queue (the spec-only route table) must not become a
	// non-nil interface in any of its consumers.
	var (
		reloads  svuc.ReloadQueue
		upgrades htemplates.RolloutQueue
		rollouts hrollouts.Queue
	)
	if d.Rollouts != nil {
		reloads, upgrades, rollouts = d.Rollouts, d.Rollouts, d.Rollouts
	}
	sharedVarsAPI := hsharedvars.NewHandler(&hsharedvars.Deps{
		Logger:              d.Log,
		Repo:                d.SharedVariableRepository,
		Servers:             d.ServerRepository,
		Users:               d.UserService,
		CommandDispatcher:   d.CommandDispatcher,
		NotificationManager: d.NotificationManager,
		StatusCache:         d.StatusCache,
		Reloads:             reloads,
	})

	templatesAPI := htemplates.NewHandler(&htemplates.Deps{
		Logger:                  d.Log,
		Templates:               d.TemplateRepository,
		SharedVariables:         d.SharedVariableRepository,
		Users:                   d.UserService,
		Servers:                 d.ServerRepository,
		CommandDispatcher:       d.CommandDispatcher,
		AppRepository:           d.AppRepository,
		AppDomainRepository:     d.AppDomainRepository,
		ServerDomainRepository:  d.ServerDomainRepository,
		ServerStorageRepository: d.ServerStorageRepository,
		StatusCache:             d.StatusCache,
		Rollouts:                upgrades,
		Instances:               d.Instances,
	})
	rolloutsAPI := hrollouts.NewHandler(&hrollouts.Deps{Logger: d.Log, Users: d.UserService, Rollouts: rollouts})

	routes := []api.Route{{
		Method: http.MethodGet, Path: "/_/health", Handler: health, Auth: api.Public,
		Summary: "Liveness probe", Tag: "system",
	}}
	for _, table := range [][]api.Route{
		enrollAPI.Routes(g),
		authAPI.Routes(g),
		notificationAPI.Routes(g),
		appsAPI.Routes(g),
		dockerAPI.Routes(g),
		serversAPI.Routes(g),
		monitoringAPI.Routes(g),
		alertsAPI.Routes(g),
		usersAPI.Routes(g),
		orgAPI.Routes(g),
		sharedVarsAPI.Routes(g),
		templatesAPI.Routes(g),
		rolloutsAPI.Routes(g),
		authprvd.Routes(),
	} {
		routes = append(routes, table...)
	}
	return routes
}

// health is the liveness probe: always 200 {"success":true,"message":"healthy"}.
func health(w http.ResponseWriter, _ *http.Request) {
	util.Success(w, "healthy", nil)
}
