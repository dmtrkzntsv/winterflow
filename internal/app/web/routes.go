package web

import (
	"net/http"
	happ "winterflow/internal/app/web/handler/app"
	"winterflow/internal/app/web/handler/server"
	webstream "winterflow/internal/app/web/handler/stream"
	"winterflow/internal/app/web/util"
	srvstream "winterflow/internal/domain/service/stream"
)

func (ro *Routing) registerRoutes() {
	ro.Router.Get("/_/health", func(w http.ResponseWriter, r *http.Request) {
		util.Success(w, "healthy", nil)
	})

	authRoutes, avaRoutes := ro.Auth.Handlers()
	ro.Router.Mount("/auth", authRoutes)  // add auth handlers
	ro.Router.Mount("/avatar", avaRoutes) // add avatar handler

	amw := ro.Auth.Middleware()
	stream := webstream.NewHandler(&webstream.Deps{
		Logger:        ro.Logger,
		StreamManager: srvstream.NewStreamManager(),
	})
	ro.Router.Get("/api/v1/stream", stream.Stream)

	appsAPI := happ.NewHandler(&happ.Deps{
		Logger:  ro.Logger,
		AppRepo: ro.Factory.NewAppRepository(),
	})
	ro.Router.With(amw.Auth, happ.GetAppsValidationMiddleware).Get("/api/v1/app/get-apps", appsAPI.GetApps)

	serversAPI := server.NewHandler(&server.Deps{
		Logger:     ro.Logger,
		ServerRepo: ro.Factory.NewServerRepository(),
	})
	ro.Router.With(amw.Auth).Get("/api/v1/server/get-servers", serversAPI.GetServers)
}
