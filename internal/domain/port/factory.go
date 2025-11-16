package port

type AppFactory interface {
	NewServerService() ServerService
	NewServerRepository() ServerRepository

	NewUserService() UserService
	NewUserRepository() UserRepository

	NewAppService() AppService
	NewAppRepository() AppRepository
}
