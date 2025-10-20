package port

type AppFactory interface {
	NewServerRepository() ServerRepository

	NewUserService() UserService
	NewUserRepository() UserRepository

	NewAppService() AppService
	NewAppRepository() AppRepository
}
