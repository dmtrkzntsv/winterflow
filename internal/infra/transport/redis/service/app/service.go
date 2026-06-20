package app

import (
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/mem/service/reply"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// BusAppService implements port.AppService over a message bus: it publishes
// commands onto the request queue and awaits the agent's reply via the
// reply.Manager. It is bus-agnostic — the same type backs the distributed
// (Redis) and standalone (in-process) topologies through the bus.Bus interface.
func NewAppService(log *logger.Logger, cfg *config.ServerConfig, b bus.Bus, rm *reply.Manager) *BusAppService {
	return &BusAppService{
		log: log,
		cfg: cfg,
		bus: b,
		rm:  rm,
	}
}

type BusAppService struct {
	rm  *reply.Manager
	bus bus.Bus
	log *logger.Logger
	cfg *config.ServerConfig
}
