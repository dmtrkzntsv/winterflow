package port

type ServerRepository interface {
	GetServers() error
}
