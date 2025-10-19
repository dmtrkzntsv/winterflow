package bootstrap

import (
	"winterflow/internal/domain/port"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

type StandaloneFactory struct {
	log *logger.Logger
	cfg *config.Config
}

func NewStandaloneFactory(log *logger.Logger, cfg *config.Config) *StandaloneFactory {
	return &StandaloneFactory{
		log: log,
		cfg: cfg,
	}
}

func (sf *StandaloneFactory) NewServerRepository() port.ServerRepository {
	return nil
}

func (sf *StandaloneFactory) NewAppRepository() port.AppRepository {
	return nil
}

func (sf *StandaloneFactory) NewAppService() port.AppService {
	return nil
}
