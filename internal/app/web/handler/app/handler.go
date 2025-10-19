package server

import (
	"winterflow/internal/domain/port"
	useapp "winterflow/internal/domain/usecase/app"
	"winterflow/pkg/logger"
)

type Handler struct {
	usecase useapp.UseCase
}

type Deps struct {
	Logger              *logger.Logger
	AppService          port.AppService
	NotificationManager port.NotificationManager
}

func NewHandler(d *Deps) *Handler {
	uc := useapp.NewUseCase(&useapp.Deps{
		AppService:          d.AppService,
		Log:                 d.Logger,
		NotificationManager: d.NotificationManager,
	})
	return &Handler{usecase: *uc}
}
