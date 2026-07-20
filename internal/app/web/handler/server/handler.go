package server

import (
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

type Handler struct {
	users   port.UserService
	servers port.ServerRepository
	status  *status.Cache
	cfg     *config.ServerConfig
	log     *logger.Logger
}

type Deps struct {
	Logger           *logger.Logger
	ServerRepository port.ServerRepository
	UserService      port.UserService
	StatusCache      *status.Cache
	Cfg              *config.ServerConfig
}

func NewHandler(d *Deps) *Handler {
	return &Handler{
		users:   d.UserService,
		servers: d.ServerRepository,
		status:  d.StatusCache,
		cfg:     d.Cfg,
		log:     d.Logger,
	}
}
