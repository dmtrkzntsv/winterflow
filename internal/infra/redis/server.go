package redis

func NewServerRepository() *RedisServerRepository {
	return &RedisServerRepository{}
}

type RedisServerRepository struct {
}

func (r *RedisServerRepository) GetServers() error {
	return nil
}
