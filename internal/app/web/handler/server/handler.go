package server

import (
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/server"
	"winterflow/pkg/logger"
)

type Handler struct {
	serverRepo port.ServerRepository
}

type Deps struct {
	Logger     *logger.Logger
	ServerRepo port.ServerRepository
}

func NewHandler(d *Deps) *Handler {
	server.NewUseCase(&server.Deps{
		ServerRepo: d.ServerRepo,
	})
	return &Handler{serverRepo: d.ServerRepo}
}
