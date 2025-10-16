package redis

func NewServerRepository() *FSServerRepository {
	return &FSServerRepository{}
}

type FSServerRepository struct {
}

func (r *FSServerRepository) GetServers() error {
	return nil
}
