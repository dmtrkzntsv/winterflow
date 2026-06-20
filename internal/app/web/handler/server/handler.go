package server

import (
	"winterflow/internal/domain/port"
	usesrv "winterflow/internal/domain/usecase/server"
	"winterflow/pkg/logger"
)

type Handler struct {
	usecase usesrv.UseCase
	users   port.UserService
	log     *logger.Logger
}

type Deps struct {
	Logger              *logger.Logger
	ServerService       port.ServerService
	UserService         port.UserService
	NotificationManager port.NotificationManager
}

func NewHandler(d *Deps) *Handler {
	uc := usesrv.NewUseCase(&usesrv.Deps{
		ServerService:       d.ServerService,
		Log:                 d.Logger,
		NotificationManager: d.NotificationManager,
	})
	return &Handler{usecase: *uc, users: d.UserService, log: d.Logger}
}
