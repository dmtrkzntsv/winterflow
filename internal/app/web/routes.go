package web

import (
	"net/http"
	"winterflow/internal/app/web/handler/server"
	webstream "winterflow/internal/app/web/handler/stream"
	"winterflow/internal/app/web/util"
	srvstream "winterflow/internal/domain/service/stream"
)

func (d *Dispatcher) RegisterRoutes() {
	d.Register("GET", "/_/health", func(w http.ResponseWriter, r *http.Request) {
		util.Success(w, "healthy", nil)
	})

	stream := webstream.NewHandler(&webstream.Deps{
		Logger:        d.log,
		StreamManager: srvstream.NewStreamManager(),
	})
	d.Register("GET", "/api/v1/stream", stream.Stream)

	serverAPI := server.NewHandler(&server.Deps{
		ServerRepo: d.factory.NewServerRepository(),
	})
	d.Register("GET", "/api/v1/server/get-servers", serverAPI.GetServers)
}
