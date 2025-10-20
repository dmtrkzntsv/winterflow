package port

type AppFactory interface {
	NewServerRepository() ServerRepository
	NewAppRepository() AppRepository
	NewUserRepository() UserRepository
	NewAppService() AppService
}
