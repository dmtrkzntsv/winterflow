package web

import (
	"context"
	"net/http"
	"slices"
	"winterflow/internal/app/web/spa"
	"winterflow/internal/domain/port"
	webdist "winterflow/web"

	"winterflow/internal/app/web/api"
	hagenthub "winterflow/internal/app/web/handler/agenthub"
	halerts "winterflow/internal/app/web/handler/alerts"
	happ "winterflow/internal/app/web/handler/app"
	hauth "winterflow/internal/app/web/handler/auth"
	hauthserver "winterflow/internal/app/web/handler/authserver"
	hdocker "winterflow/internal/app/web/handler/docker"
	henroll "winterflow/internal/app/web/handler/enroll"
	hhubjoin "winterflow/internal/app/web/handler/hubjoin"
	"winterflow/internal/app/web/handler/monitoring"
	"winterflow/internal/app/web/handler/notification"
	hoauthlogin "winterflow/internal/app/web/handler/oauthlogin"
	horg "winterflow/internal/app/web/handler/org"
	hrollouts "winterflow/internal/app/web/handler/rollouts"
	hsecretreq "winterflow/internal/app/web/handler/secretreq"
	"winterflow/internal/app/web/handler/server"
	hsharedvars "winterflow/internal/app/web/handler/sharedvars"
	htemplates "winterflow/internal/app/web/handler/templates"
	huser "winterflow/internal/app/web/handler/user"
	webmcp "winterflow/internal/app/web/mcp"
	mcptools "winterflow/internal/app/web/mcp/tools"
	"winterflow/internal/app/web/middleware/authn"
	"winterflow/internal/app/web/middleware/ratelimit"
	"winterflow/internal/app/web/middleware/userlimit"
	"winterflow/internal/app/web/util"
	svuc "winterflow/internal/domain/usecase/sharedvars"
	"winterflow/internal/infra/bootstrap"
)

func (s *Server) registerRoutes() {
	// Login and register are the API's only password-priced requests —
	// throttle them per client IP so credential floods answer 429 instead of
	// burning CPU. One limiter is shared by both.
	authLimiter := ratelimit.New(5, 10)

	// authMW resolves Bearer, Basic (token as password) or the session
	// cookie; OAuth tokens are refused here (they are for /mcp only).
	authMW := authn.Middleware(s.Deps.Authn, authn.Options{})
	// No role guard: every org member may do everything; server-addressed
	// handlers still check the caller's org owns the server.
	enrollLimiter := ratelimit.New(5, 30)
	// Starting an enrollment stores a row: about 3 a minute per IP, burst 5.
	enrollStartLimiter := ratelimit.New(0.05, 5)
	// OAuth client registration is open and stores a row: one every 5 s
	// per IP, burst 10.
	oauthRegisterLimiter := ratelimit.New(0.2, 10)
	// /mcp is limited per user, not per IP: MCP clients run by an AI
	// provider share its egress IPs. 10 calls a second, burst 30. The
	// secret link page's routes share it (per user, too).
	mcpUserLimiter := userlimit.New(10, 30)
	guards := api.Guards{AuthLimit: authLimiter.Middleware, EnrollLimit: enrollLimiter.Middleware,
		EnrollStartLimit: enrollStartLimiter.Middleware,
		// The OAuth endpoints' clients read JSON errors cross-origin, so
		// their limiters answer in that format (the token endpoint shares
		// the auth bucket).
		OAuthRegisterLimit: oauthRegisterLimiter.MiddlewareWith(hauthserver.TooManyRegistrations),
		OAuthTokenLimit:    authLimiter.MiddlewareWith(hauthserver.TooManyTokenRequests),
		UserLimit:          mcpUserLimiter.Middleware}
	routes := routeTable(s.Deps, guards)
	if s.Deps.Enrollment == nil {
		// Standalone: agents do not enroll by code and there are no hubs to
		// join or move between; the routes answer 404.
		routes = slices.DeleteFunc(routes, func(rt api.Route) bool { return rt.Tag == "enroll" })
	}
	// The hub join and agent hub routes are mounted only where their
	// usecase is wired (a nil one would panic on the first request).
	if s.Deps.HubJoin == nil {
		routes = slices.DeleteFunc(routes, func(rt api.Route) bool { return rt.Path == hhubjoin.EnrollPath })
	}
	if s.Deps.AgentHub == nil {
		routes = slices.DeleteFunc(routes, func(rt api.Route) bool { return rt.Path == hagenthub.HubPath })
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

// routeTable collects every handler package's routes, plus the health probe.
// The NotificationManager is shared
// across handlers: the SSE stream subscribes to it and the usecases publish
// to it, so every handler MUST receive the same instance from d.
func routeTable(d *bootstrap.Deps, g api.Guards) []api.Route {
	var afterSignIn func(context.Context, string)
	if d.Cfg != nil && d.Cfg.IsStandalone() {
		// The embedded agent self-registered at boot with a pairing code but
		// belongs to no org: claim it into the signing-in user's org, so the
		// server "just appears".
		afterSignIn = func(ctx context.Context, userID string) { autoClaimStandaloneServer(ctx, d, userID) }
	}
	// Typed nils must not become non-nil interfaces: the spec-only route
	// table has no sign-in usecase.
	var (
		signIns   hoauthlogin.SignIns
		providers hauth.ProviderLister
	)
	if d.SignIns != nil {
		signIns, providers = d.SignIns, d.SignIns
	}
	authAPI := hauth.NewHandler(&hauth.Deps{
		Logger: d.Log, Cfg: d.Cfg, Users: d.UserService, Sessions: d.Authn, AfterSignIn: afterSignIn,
		Providers: providers, OAuthServer: d.AuthServer.Enabled(),
	})
	authServerAPI := hauthserver.NewHandler(&hauthserver.Deps{Logger: d.Log, Server: d.AuthServer})
	var sessionAuth authn.Authenticator
	if d.Authn != nil {
		sessionAuth = d.Authn
	}
	oauthLoginAPI := hoauthlogin.NewHandler(&hoauthlogin.Deps{
		Logger: d.Log, SignIns: signIns, Sessions: authAPI, Auth: sessionAuth,
		Registration:  func(ctx context.Context) (bool, bool) { return registrationOpen(ctx, d) },
		SecureCookies: d.Cfg != nil && d.Cfg.GetSecureCookies(),
	})
	notificationAPI := notification.NewHandler(&notification.Deps{
		Logger:              d.Log,
		NotificationManager: d.NotificationManager,
		Auth:                sessionAuth,
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
	var hubJoiner hhubjoin.Joiner
	if d.HubJoin != nil {
		hubJoiner = d.HubJoin
	}
	enrollAPI := henroll.NewHandler(&henroll.Deps{Logger: d.Log, Enrollments: enrollments})
	hubJoinAPI := hhubjoin.NewHandler(&hhubjoin.Deps{Logger: d.Log, Joiner: hubJoiner})
	var locator hagenthub.Locator
	if d.AgentHub != nil {
		locator = d.AgentHub
	}
	agentHubAPI := hagenthub.NewHandler(&hagenthub.Deps{Logger: d.Log, Locator: locator})
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
	// Typed nils must not become non-nil interfaces: without ENCRYPTION_KEY
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
	usersAPI := huser.NewHandler(&huser.Deps{
		Logger: d.Log, Tokens: d.Authn, Users: d.UserService,
		SecureCookies: d.Cfg != nil && d.Cfg.GetSecureCookies(),
	})
	orgAPI := horg.NewHandler(&horg.Deps{Logger: d.Log, Users: d.UserService, Tokens: d.Authn})
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
	// Typed nils must not become non-nil interfaces: the spec-only route
	// table has no secret links usecase or server repository.
	var (
		secretRequests hsecretreq.Requests
		keys           util.CapabilityReader
	)
	if d.SecretRequests != nil {
		secretRequests = d.SecretRequests
	}
	if d.ServerRepository != nil {
		keys = d.ServerRepository
	}
	secretsAPI := hsecretreq.NewHandler(&hsecretreq.Deps{Logger: d.Log, Requests: secretRequests, Keys: keys, Cfg: d.Cfg})
	mcpRoutes := webmcp.Routes(webmcp.Edge{
		Auth: sessionAuth, AllowOAuth: d.AuthServer.Enabled(), Challenge: d.AuthServer.Challenge(),
		Register: mcptools.New(mcptools.FromDeps(d)),
	}, g)

	routes := []api.Route{{
		Method: http.MethodGet, Path: "/_/health", Handler: health, Auth: api.Public,
		Summary: "Liveness probe", Tag: "system",
	}}
	for _, table := range [][]api.Route{
		enrollAPI.Routes(g),
		hubJoinAPI.Routes(g),
		agentHubAPI.Routes(g),
		authAPI.Routes(g),
		oauthLoginAPI.Routes(g),
		authServerAPI.Routes(g),
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
		secretsAPI.Routes(g),
		mcpRoutes,
	} {
		routes = append(routes, table...)
	}
	return routes
}

// health is the liveness probe: always 200 {"success":true,"message":"healthy"}.
func health(w http.ResponseWriter, _ *http.Request) {
	util.Success(w, "healthy", nil)
}
