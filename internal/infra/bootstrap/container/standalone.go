package container

import (
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/cert"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/repository"
	"winterflow/internal/infra/db/service"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

type StandaloneContainer struct {
	Factory *StandaloneFactory
	Cert    *cert.Manager
}

func (c *StandaloneContainer) GetAppFactory() *StandaloneFactory {
	return c.Factory
}

type StandaloneFactory struct {
	Log *logger.Logger
	Cfg *config.ServerConfig
	Db  *db.BunConnection
}

func (f *StandaloneFactory) NewUserService() port.UserService {
	return service.NewDbUserService(f.Log, repository.NewDbUserRepository(f.Db, f.Log))
}

func (f *StandaloneFactory) NewServerRepository() port.ServerRepository {
	return repository.NewDbServerRepository(f.Db, f.Log)
}

func (f *StandaloneFactory) NewServerService() port.ServerService {
	return nil
}

func (f *StandaloneFactory) NewAppRepository() port.AppRepository {
	return nil
}

func (f *StandaloneFactory) NewUserRepository() port.UserRepository {
	return repository.NewDbUserRepository(f.Db, f.Log)
}

func (f *StandaloneFactory) NewAppService() port.AppService {
	return nil
}
