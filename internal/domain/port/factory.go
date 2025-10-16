package port

type Factory interface {
	NewServerRepository() ServerRepository
	NewAppRepository() AppRepository
}
