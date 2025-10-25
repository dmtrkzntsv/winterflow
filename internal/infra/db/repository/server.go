package repository

import "winterflow/internal/domain/model"

func NewDbServerRepository() *DbServerRepository {
	return &DbServerRepository{}
}

type DbServerRepository struct {
}

func (r *DbServerRepository) GetServers() ([]model.Agent, error) {
	return make([]model.Agent, 0), nil
}
