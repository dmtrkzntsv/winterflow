package app

import (
	"context"
	"encoding/json"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

type UseCase struct {
	dispatcher port.CommandDispatcher
	repo       port.AppRepository
	log        *logger.Logger
}

type Deps struct {
	CommandDispatcher port.CommandDispatcher
	AppRepository     port.AppRepository
	Log               *logger.Logger
}

func NewUseCase(d *Deps) *UseCase {
	return &UseCase{
		dispatcher: d.CommandDispatcher,
		repo:       d.AppRepository,
		log:        d.Log,
	}
}

// GetApps returns the app list from the DB cache (API-local, synchronous). The
// agent-authoritative reconcile happens separately via the apps.list command.
func (uc *UseCase) GetApps(ctx context.Context, serverID string) ([]model.App, error) {
	return uc.repo.GetApps(ctx, serverID)
}

// CreateApp dispatches an app.save command to the server's agent and returns
// the request id immediately. The agent's result is delivered to the user over
// SSE (and persisted to the DB) by the bus response subscriber — this call does
// not block.
func (uc *UseCase) CreateApp(ctx context.Context, userID, serverID string, app model.App) (string, error) {
	cfgBytes, _ := json.Marshal(app)
	req := command.SaveAppRequest{
		App: command.AppPayload{
			AppID:  app.ID,
			Config: cfgBytes,
		},
	}
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppSave,
		Payload: req,
	})
}
