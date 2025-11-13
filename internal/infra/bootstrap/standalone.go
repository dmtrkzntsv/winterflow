package bootstrap

import (
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/repository"
	"winterflow/internal/infra/db/service"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

type StandaloneContainer struct {
	factory *StandaloneFactory
}

func (c *StandaloneContainer) GetAppFactory() *StandaloneFactory {
	return c.factory
}

type StandaloneFactory struct {
	log *logger.Logger
	cfg *config.Config
	db  *db.BunConnection
}

func BootstrapStandalone(log *logger.Logger, cfg *config.Config) *StandaloneContainer {
	dbconn := db.NewBunConnection(log, cfg.GetDbURL())
	return &StandaloneContainer{
		factory: &StandaloneFactory{
			log: log,
			cfg: cfg,
			db:  dbconn,
		},
	}
}

func (f *StandaloneFactory) NewUserService() port.UserService {
	return service.NewDbUserService(f.log, repository.NewDbUserRepository(f.db, f.log))
}

func (f *StandaloneFactory) NewServerRepository() port.ServerRepository {
	return nil
}

func (f *StandaloneFactory) NewAppRepository() port.AppRepository {
	return nil
}

func (f *StandaloneFactory) NewUserRepository() port.UserRepository {
	return repository.NewDbUserRepository(f.db, f.log)
}

func (f *StandaloneFactory) NewAppService() port.AppService {
	return nil
}
