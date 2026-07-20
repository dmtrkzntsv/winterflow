package server

import (
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	useapp "winterflow/internal/domain/usecase/app"
	"winterflow/pkg/logger"
)

type Handler struct {
	usecase useapp.UseCase
	status  *status.Cache
}

type Deps struct {
	Logger            *logger.Logger
	CommandDispatcher port.CommandDispatcher
	AppRepository     port.AppRepository
	StatusCache       *status.Cache
}

func NewHandler(d *Deps) *Handler {
	uc := useapp.NewUseCase(&useapp.Deps{
		CommandDispatcher: d.CommandDispatcher,
		AppRepository:     d.AppRepository,
		Log:               d.Logger,
	})
	return &Handler{usecase: *uc, status: d.StatusCache}
}
