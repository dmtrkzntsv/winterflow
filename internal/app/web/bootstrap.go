package web

import (
	"net/http"
	"time"
	"winterflow/internal/app"
	corsmw "winterflow/internal/app/web/middleware/cors"
	logmw "winterflow/internal/app/web/middleware/logger"
	timeoutmw "winterflow/internal/app/web/middleware/timeout"
	"winterflow/internal/infra/bootstrap"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func NewServer(mode app.AppMode, log *logger.Logger, cfg *config.Config) *http.Server {
	d := NewDispatcher(Deps{
		Logger:  log,
		Cfg:     cfg,
		Factory: bootstrap.NewFactory(mode),
	})

	d.Use(logmw.WithLogger(log), timeoutmw.WithTimeout(cfg.GetRouteTimeout()), corsmw.UseCORS(cfg.GetAllowedOrigins()))
	d.RegisterRoutes()

	return &http.Server{
		Addr:         ":" + cfg.GetServerPort(),
		Handler:      d,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}
