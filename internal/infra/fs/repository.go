package redis

import "winterflow/internal/domain/model"

func NewFSServerRepository() *FSServerRepository {
	return &FSServerRepository{}
}

type FSServerRepository struct {
}

func (r *FSServerRepository) GetServers() ([]model.Server, error) {
	return make([]model.Server, 0), nil
}
