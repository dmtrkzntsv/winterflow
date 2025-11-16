package hub

import (
	"winterflow/internal/domain/port"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

type Hub struct {
	Logger  *logger.Logger
	Cfg     *config.ServerConfig
	Factory port.AppFactory
}

func NewHUB(log *logger.Logger, cfg *config.ServerConfig, factory port.AppFactory) *Hub {
	return &Hub{
		Logger:  log,
		Cfg:     cfg,
		Factory: factory,
	}
}
