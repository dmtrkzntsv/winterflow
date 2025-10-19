package app

import (
	"winterflow/internal/infra/mem/service/reply"
	redisbus "winterflow/internal/infra/redis/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func NewAppService(log *logger.Logger, cfg *config.Config, bus *redisbus.Bus, rm *reply.Manager) *RedisAppService {
	return &RedisAppService{
		log: log,
		cfg: cfg,
		bus: bus,
		rm:  rm,
	}
}

type RedisAppService struct {
	rm  *reply.Manager
	bus *redisbus.Bus
	log *logger.Logger
	cfg *config.Config
}
