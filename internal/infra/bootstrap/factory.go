package bootstrap

import (
	"winterflow/internal/domain/port"
	infrafs "winterflow/internal/infra/fs"
	infraredis "winterflow/internal/infra/redis"
)

type AppMode string

const (
	AppModeStandalone AppMode = "standalone"
)

type Factory struct {
	mode AppMode
}

func NewStandaloneFactory() *Factory {
	return &Factory{
		mode: AppModeStandalone,
	}
}

func (sf *Factory) NewServerRepository() port.ServerRepository {
	if sf.mode == AppModeStandalone {
		return infrafs.NewFSServerRepository()
	}
	return infraredis.NewRedisServerRepository()
}
