package controller

import (
	"net/http"
	"winterflow/internal/app/controller/handler/server"
	"winterflow/internal/app/controller/util"
)

func (d *Dispatcher) RegisterRoutes() {
	d.Register("GET", "/_/health", func(w http.ResponseWriter, r *http.Request) {
		util.Success(w, "healthy", nil)
	})

	serverAPI := server.NewHandler(&server.Deps{})
	d.Register("GET", "/api/v1/server/get-servers", serverAPI.GetServers)
}
