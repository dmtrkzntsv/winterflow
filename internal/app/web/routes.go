package web

import (
	"net/http"
	happ "winterflow/internal/app/web/handler/app"
	hauth "winterflow/internal/app/web/handler/auth"
	hdocker "winterflow/internal/app/web/handler/docker"
	"winterflow/internal/app/web/handler/notification"
	horg "winterflow/internal/app/web/handler/org"
	"winterflow/internal/app/web/handler/server"
	huser "winterflow/internal/app/web/handler/user"
	"winterflow/internal/app/web/middleware/patauth"
	"winterflow/internal/app/web/middleware/rbac"
	"winterflow/internal/app/web/util"
)

func (s *Server) registerRoutes() {
	s.Router.Get("/_/health", func(w http.ResponseWriter, r *http.Request) {
		util.Success(w, "healthy", nil)
	})

	authRoutes, avaRoutes := s.Auth.Handlers()
	s.Router.Mount("/auth", authRoutes)  // add auth handlers
	s.Router.Mount("/avatar", avaRoutes) // add avatar handler

	amw := s.Auth.Middleware()
	// authMW = PAT bearer first, JWT session otherwise. Every /api/v1 route
	// uses this so tokens work everywhere a browser session does.
	authMW := patauth.Middleware(s.Deps.UserService, amw.Auth)
	// adminMW gates administration (member management, server registration,
	// infrastructure mutations) to owners/admins. Members keep the full app
	// lifecycle.
	adminMW := rbac.RequireAdmin(s.Deps.UserService)

	// Public auth surface: registration (accounts are only created here or
	// by an admin) and the state the login/register pages render from.
	authAPI := hauth.NewHandler(&hauth.Deps{
		Logger: s.Logger,
		Cfg:    s.Cfg,
		Users:  s.Deps.UserService,
	})
	s.Router.Post("/api/v1/auth/register", authAPI.Register)
	s.Router.Get("/api/v1/auth/state", authAPI.State)

	// The NotificationManager is shared across every handler: the SSE stream
	// subscribes to it and the usecases publish to it, so async results reach
	// the browser. Each handler MUST receive the same instance from Deps.
	notificationAPI := notification.NewHandler(&notification.Deps{
		Logger:              s.Logger,
		NotificationManager: s.Deps.NotificationManager,
	})
	s.Router.With(authMW).Get("/api/v1/sse", notificationAPI.Stream)

	appsAPI := happ.NewHandler(&happ.Deps{
		Logger:              s.Logger,
		CommandDispatcher:   s.Deps.CommandDispatcher,
		AppRepository:       s.Deps.AppRepository,
		AppDomainRepository: s.Deps.AppDomainRepository,
		StatusCache:         s.Deps.StatusCache,
	})
	s.Router.With(authMW, happ.GetAppsValidationMiddleware).Get("/api/v1/app/get-apps", appsAPI.GetApps)
	s.Router.With(authMW, happ.GetAppsValidationMiddleware).Get("/api/v1/app/get-apps-status", appsAPI.GetAppsStatus)
	s.Router.With(authMW, happ.GetAppsValidationMiddleware).Post("/api/v1/app/refresh-apps", appsAPI.RefreshApps)
	s.Router.With(authMW).Post("/api/v1/app/save-app", appsAPI.SaveApp)
	s.Router.With(authMW).Get("/api/v1/app/get-app", appsAPI.GetApp)
	s.Router.With(authMW).Get("/api/v1/app/get-logs", appsAPI.GetLogs)
	s.Router.With(authMW).Get("/api/v1/app/get-revisions", appsAPI.GetRevisions)
	s.Router.With(authMW).Post("/api/v1/app/rollback-app", appsAPI.RollbackApp)
	s.Router.With(authMW).Get("/api/v1/image/get-tags", appsAPI.GetImageTags)
	s.Router.With(authMW).Post("/api/v1/app/control-app", appsAPI.ControlApp)
	s.Router.With(authMW).Post("/api/v1/app/delete-app", appsAPI.DeleteApp)
	s.Router.With(authMW).Post("/api/v1/app/rename-app", appsAPI.RenameApp)
	s.Router.With(authMW).Get("/api/v1/domains/check", appsAPI.CheckDomain)

	dockerAPI := hdocker.NewHandler(&hdocker.Deps{
		Logger:            s.Logger,
		CommandDispatcher: s.Deps.CommandDispatcher,
	})
	s.Router.With(authMW).Get("/api/v1/registry/list", dockerAPI.ListRegistries)
	s.Router.With(authMW, adminMW).Post("/api/v1/registry/create", dockerAPI.CreateRegistry)
	s.Router.With(authMW, adminMW).Post("/api/v1/registry/delete", dockerAPI.DeleteRegistry)
	s.Router.With(authMW).Get("/api/v1/network/list", dockerAPI.ListNetworks)
	s.Router.With(authMW, adminMW).Post("/api/v1/network/create", dockerAPI.CreateNetwork)
	s.Router.With(authMW, adminMW).Post("/api/v1/network/delete", dockerAPI.DeleteNetwork)
	s.Router.With(authMW, adminMW).Post("/api/v1/agent/update", dockerAPI.UpdateAgent)

	serversAPI := server.NewHandler(&server.Deps{
		Logger:           s.Logger,
		ServerRepository: s.Deps.ServerRepository,
		UserService:      s.Deps.UserService,
		StatusCache:      s.Deps.StatusCache,
		Cfg:              s.Deps.Cfg,
	})
	s.Router.With(authMW).Get("/api/v1/server/get-servers", serversAPI.GetServers)
	s.Router.With(authMW).Get("/api/v1/server/get-servers-status", serversAPI.GetServersStatus)
	s.Router.With(authMW).Get("/api/v1/server/get-public-key", serversAPI.GetPublicKey)
	s.Router.With(authMW, adminMW).Post("/api/v1/server/register", serversAPI.Register)

	usersAPI := huser.NewHandler(&huser.Deps{
		Logger: s.Logger,
		Tokens: s.Deps.UserService,
		Users:  s.Deps.UserService,
	})
	s.Router.With(authMW).Post("/api/v1/user/create-token", usersAPI.CreateToken)
	s.Router.With(authMW).Get("/api/v1/user/get-tokens", usersAPI.GetTokens)
	s.Router.With(authMW).Post("/api/v1/user/delete-token", usersAPI.DeleteToken)
	s.Router.With(authMW).Get("/api/v1/user/get-profile", usersAPI.GetProfile)
	s.Router.With(authMW).Post("/api/v1/user/change-password", usersAPI.ChangePassword)

	orgAPI := horg.NewHandler(&horg.Deps{
		Logger: s.Logger,
		Users:  s.Deps.UserService,
	})
	s.Router.With(authMW, adminMW).Post("/api/v1/org/create-user", orgAPI.CreateUser)
	s.Router.With(authMW, adminMW).Get("/api/v1/org/get-members", orgAPI.GetMembers)
	s.Router.With(authMW, adminMW).Post("/api/v1/org/update-member", orgAPI.UpdateMember)
	s.Router.With(authMW, adminMW).Post("/api/v1/org/remove-member", orgAPI.RemoveMember)
	s.Router.With(authMW, adminMW).Post("/api/v1/org/reset-member-password", orgAPI.ResetMemberPassword)
	s.Router.With(authMW).Get("/api/v1/org/get-organization", orgAPI.GetOrganization)
	s.Router.With(authMW, adminMW).Post("/api/v1/org/update-organization", orgAPI.UpdateOrganization)
}
