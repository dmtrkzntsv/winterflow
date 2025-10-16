package web

import (
	"net/http"
	"winterflow/internal/app/web/handler/server"
	"winterflow/internal/app/web/util"
)

func (d *Dispatcher) RegisterRoutes() {
	d.Register("GET", "/_/health", func(w http.ResponseWriter, r *http.Request) {
		util.Success(w, "healthy", nil)
	})

	serverAPI := server.NewHandler(&server.Deps{
		ServerRepo: d.factory.NewServerRepository(),
	})
	d.Register("GET", "/api/v1/server/get-servers", serverAPI.GetServers)
}
