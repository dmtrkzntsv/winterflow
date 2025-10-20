package web

import (
	"net/http"
	happ "winterflow/internal/app/web/handler/app"
	"winterflow/internal/app/web/handler/notification"
	"winterflow/internal/app/web/handler/server"
	"winterflow/internal/app/web/util"
	nm "winterflow/internal/domain/service/notification"
)

func (s *Server) registerRoutes() {
	s.Router.Get("/_/health", func(w http.ResponseWriter, r *http.Request) {
		util.Success(w, "healthy", nil)
	})

	authRoutes, avaRoutes := s.Auth.Handlers()
	s.Router.Mount("/auth", authRoutes)  // add auth handlers
	s.Router.Mount("/avatar", avaRoutes) // add avatar handler

	amw := s.Auth.Middleware()
	notificationAPI := notification.NewHandler(&notification.Deps{
		Logger:              s.Logger,
		NotificationManager: nm.NewNotificationManager(),
	})
	s.Router.With(amw.Auth).Get("/api/v1/notification/stream", notificationAPI.Stream)

	appsAPI := happ.NewHandler(&happ.Deps{
		Logger:              s.Logger,
		AppService:          s.Factory.NewAppService(),
		NotificationManager: nm.NewNotificationManager(),
	})
	s.Router.With(amw.Auth, happ.GetAppsValidationMiddleware).Get("/api/v1/app/get-apps", appsAPI.GetApps)

	serversAPI := server.NewHandler(&server.Deps{
		Logger:     s.Logger,
		ServerRepo: s.Factory.NewServerRepository(),
	})
	s.Router.With(amw.Auth).Get("/api/v1/server/get-servers", serversAPI.GetServers)
}
