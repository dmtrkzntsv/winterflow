package repository

import "winterflow/internal/domain/model"

func NewRedisAppRepository() *RedisAppRepository {
	return &RedisAppRepository{}
}

type RedisAppRepository struct {
}

func (r *RedisAppRepository) GetApps(serverID string) ([]model.App, error) {
	return make([]model.App, 0), nil
}
