package server

import (
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	usesrv "winterflow/internal/domain/usecase/server"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

type Handler struct {
	usecase usesrv.UseCase
	users   port.UserService
	servers port.ServerRepository
	status  *status.Cache
	cfg     *config.ServerConfig
	log     *logger.Logger
}

type Deps struct {
	Logger           *logger.Logger
	ServerService    port.ServerService
	ServerRepository port.ServerRepository
	UserService      port.UserService
	StatusCache      *status.Cache
	Cfg              *config.ServerConfig
}

func NewHandler(d *Deps) *Handler {
	uc := usesrv.NewUseCase(&usesrv.Deps{
		ServerService: d.ServerService,
		Log:           d.Logger,
	})
	return &Handler{
		usecase: *uc,
		users:   d.UserService,
		servers: d.ServerRepository,
		status:  d.StatusCache,
		cfg:     d.Cfg,
		log:     d.Logger,
	}
}
