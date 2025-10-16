package repository

import "winterflow/internal/domain/model"

func NewDbServerRepository() *DbServerRepository {
	return &DbServerRepository{}
}

type DbServerRepository struct {
}

func (r *DbServerRepository) GetServers() ([]model.Server, error) {
	return make([]model.Server, 0), nil
}
