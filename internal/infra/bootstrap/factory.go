package bootstrap

import (
	"winterflow/internal/app"
	"winterflow/internal/domain/port"
	infradb "winterflow/internal/infra/db/repository"
	infrafs "winterflow/internal/infra/fs/repository"
	infraredis "winterflow/internal/infra/redis/repository"
)

type Factory struct {
	mode app.AppMode
}

func NewFactory(mode app.AppMode) *Factory {
	return &Factory{
		mode: mode,
	}
}

func (sf *Factory) NewServerRepository() port.ServerRepository {
	return infradb.NewDbServerRepository()
}

func (sf *Factory) NewAppRepository() port.AppRepository {
	if sf.mode == app.AppModeStandalone {
		return infrafs.NewFsAppRepository()
	}
	return infraredis.NewRedisAppRepository()
}
