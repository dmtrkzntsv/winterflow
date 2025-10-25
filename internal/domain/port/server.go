package port

import "winterflow/internal/domain/model"

type ServerRepository interface {
	GetServers() ([]model.Agent, error)
}
