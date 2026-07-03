package web

import (
	"net/http"
	happ "winterflow/internal/app/web/handler/app"
	hdocker "winterflow/internal/app/web/handler/docker"
	"winterflow/internal/app/web/handler/notification"
	"winterflow/internal/app/web/handler/server"
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

	// The NotificationManager is shared across every handler: the SSE stream
	// subscribes to it and the usecases publish to it, so async results reach
	// the browser. Each handler MUST receive the same instance from Deps.
	notificationAPI := notification.NewHandler(&notification.Deps{
		Logger:              s.Logger,
		NotificationManager: s.Deps.NotificationManager,
	})
	s.Router.With(amw.Auth).Get("/api/v1/notification/stream", notificationAPI.Stream)

	appsAPI := happ.NewHandler(&happ.Deps{
		Logger:            s.Logger,
		CommandDispatcher: s.Deps.CommandDispatcher,
		AppRepository:     s.Deps.AppRepository,
		StatusCache:       s.Deps.StatusCache,
	})
	s.Router.With(amw.Auth, happ.GetAppsValidationMiddleware).Get("/api/v1/app/get-apps", appsAPI.GetApps)
	s.Router.With(amw.Auth, happ.GetAppsValidationMiddleware).Get("/api/v1/app/get-apps-status", appsAPI.GetAppsStatus)
	s.Router.With(amw.Auth, happ.GetAppsValidationMiddleware).Post("/api/v1/app/refresh-apps", appsAPI.RefreshApps)
	s.Router.With(amw.Auth).Post("/api/v1/app/create-app", appsAPI.CreateApp)
	s.Router.With(amw.Auth).Get("/api/v1/app/get-app", appsAPI.GetApp)
	s.Router.With(amw.Auth).Get("/api/v1/app/get-logs", appsAPI.GetLogs)
	s.Router.With(amw.Auth).Post("/api/v1/app/control-app", appsAPI.ControlApp)
	s.Router.With(amw.Auth).Post("/api/v1/app/delete-app", appsAPI.DeleteApp)
	s.Router.With(amw.Auth).Post("/api/v1/app/rename-app", appsAPI.RenameApp)

	dockerAPI := hdocker.NewHandler(&hdocker.Deps{
		Logger:            s.Logger,
		CommandDispatcher: s.Deps.CommandDispatcher,
	})
	s.Router.With(amw.Auth).Get("/api/v1/registry/list", dockerAPI.ListRegistries)
	s.Router.With(amw.Auth).Post("/api/v1/registry/create", dockerAPI.CreateRegistry)
	s.Router.With(amw.Auth).Post("/api/v1/registry/delete", dockerAPI.DeleteRegistry)
	s.Router.With(amw.Auth).Get("/api/v1/network/list", dockerAPI.ListNetworks)
	s.Router.With(amw.Auth).Post("/api/v1/network/create", dockerAPI.CreateNetwork)
	s.Router.With(amw.Auth).Post("/api/v1/network/delete", dockerAPI.DeleteNetwork)
	s.Router.With(amw.Auth).Post("/api/v1/agent/update", dockerAPI.UpdateAgent)

	serversAPI := server.NewHandler(&server.Deps{
		Logger:           s.Logger,
		ServerService:    s.Deps.ServerService,
		ServerRepository: s.Deps.ServerRepository,
		UserService:      s.Deps.UserService,
		StatusCache:      s.Deps.StatusCache,
		Cfg:              s.Deps.Cfg,
	})
	s.Router.With(amw.Auth).Get("/api/v1/server/get-servers", serversAPI.GetServers)
	s.Router.With(amw.Auth).Get("/api/v1/server/get-servers-status", serversAPI.GetServersStatus)
	s.Router.With(amw.Auth).Get("/api/v1/server/get-public-key", serversAPI.GetPublicKey)
	s.Router.With(amw.Auth).Post("/api/v1/server/register", serversAPI.Register)
}
