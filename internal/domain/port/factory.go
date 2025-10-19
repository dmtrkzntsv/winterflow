package port

type AppFactory interface {
	NewServerRepository() ServerRepository
	NewAppRepository() AppRepository
	NewAppService() AppService
}
