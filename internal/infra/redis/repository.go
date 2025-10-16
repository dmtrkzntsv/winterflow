package redis

import "winterflow/internal/domain/model"

func NewRedisServerRepository() *RedisServerRepository {
	return &RedisServerRepository{}
}

type RedisServerRepository struct {
}

func (r *RedisServerRepository) GetServers() ([]model.Server, error) {
	return make([]model.Server, 0), nil
}
