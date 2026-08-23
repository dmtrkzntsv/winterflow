package server

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	useapp "winterflow/internal/domain/usecase/app"
	"winterflow/pkg/logger"
)

type Handler struct {
	usecase useapp.UseCase
	status  *status.Cache
	servers webutil.ServerAccess
	log     *logger.Logger
}

type Deps struct {
	Logger              *logger.Logger
	CommandDispatcher   port.CommandDispatcher
	AppRepository       port.AppRepository
	AppDomainRepository port.AppDomainRepository
	StatusCache         *status.Cache
	// Servers authorizes server-addressed operations: every handler that takes
	// a server_id verifies the caller's org owns it before acting.
	Servers webutil.ServerAccess
}

func NewHandler(d *Deps) *Handler {
	uc := useapp.NewUseCase(&useapp.Deps{
		CommandDispatcher:   d.CommandDispatcher,
		AppRepository:       d.AppRepository,
		AppDomainRepository: d.AppDomainRepository,
		Log:                 d.Logger,
	})
	return &Handler{usecase: *uc, status: d.StatusCache, servers: d.Servers, log: d.Logger}
}

// authorize resolves the caller and enforces that serverID belongs to one of
// their organizations, writing 401/403 itself. The bool reports success.
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, serverID string) (string, bool) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return "", false
	}
	if !webutil.RequireServerAccess(w, r, h.servers, userID, serverID) {
		return "", false
	}
	return userID, true
}
