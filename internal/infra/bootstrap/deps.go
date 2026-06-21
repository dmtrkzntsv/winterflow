package bootstrap

import (
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// Deps holds the fully-constructed dependencies a web server needs to run.
//
// Each binary (standalone, api, …) builds a Deps with the implementations that
// fit its topology — DB-backed services for standalone, Redis/Bus-backed
// services for the distributed API — and hands the same struct to web.NewServer.
// There is intentionally no factory abstraction: wiring happens once, in plain
// constructor calls, inside the Bootstrap* functions.
type Deps struct {
	Log *logger.Logger
	Cfg *config.ServerConfig

	UserService         port.UserService
	ServerService       port.ServerService
	ServerRepository    port.ServerRepository
	AppRepository       port.AppRepository
	CommandDispatcher   port.CommandDispatcher
	NotificationManager port.NotificationManager
	StatusCache         *status.Cache
}
