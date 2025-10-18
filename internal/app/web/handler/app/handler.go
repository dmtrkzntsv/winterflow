package server

import (
	"winterflow/internal/domain/port"
	useapp "winterflow/internal/domain/usecase/app"
	"winterflow/pkg/logger"
)

type Handler struct {
	appRepo port.AppRepository
}

type Deps struct {
	Logger  *logger.Logger
	AppRepo port.AppRepository
}

func NewHandler(d *Deps) *Handler {
	useapp.NewUseCase(&useapp.Deps{
		AppRepo: d.AppRepo,
	})
	return &Handler{appRepo: d.AppRepo}
}
