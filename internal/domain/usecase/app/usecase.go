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
// agent-authoritative reconcile happens separately via RefreshApps.
func (uc *UseCase) GetApps(ctx context.Context, serverID string) ([]model.App, error) {
	return uc.repo.GetApps(ctx, serverID)
}

// RefreshApps dispatches apps.list to the server's agent (its filesystem is the
// source of truth) and returns the request id. On the agent's result the DB
// cache is reconciled (SyncApps: upsert reported, delete missing) and the
// reconciled list is delivered to the user over SSE.
func (uc *UseCase) RefreshApps(ctx context.Context, userID, serverID string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppsList,
		Payload: command.ListAppsRequest{},
		OnResult: func(res port.CommandResult) {
			if !res.Success || len(res.Payload) == 0 {
				return
			}
			var listed command.ListAppsResponse
			if err := json.Unmarshal(res.Payload, &listed); err != nil {
				uc.log.Error("RefreshApps: decode result", "error", err)
				return
			}
			if err := uc.repo.SyncApps(context.Background(), serverID, listed.Apps); err != nil {
				uc.log.Error("RefreshApps: sync", "error", err, "server_id", serverID)
			}
		},
	})
}

// CreateApp dispatches an app.save command to the server's agent and returns
// the request id immediately. This call does not block. When the agent confirms
// the save, the OnResult hook persists the app to the DB (the agent assigns the
// app id) and the result is delivered to the user over SSE.
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
		OnResult: func(res port.CommandResult) {
			if !res.Success {
				return
			}
			var saved command.SaveAppResponse
			if len(res.Payload) > 0 {
				if err := json.Unmarshal(res.Payload, &saved); err != nil {
					uc.log.Error("CreateApp: decode result", "error", err)
					return
				}
			}
			persisted := app
			persisted.ServerID = serverID
			if saved.AppID != "" {
				persisted.ID = saved.AppID
			}
			if err := uc.repo.CreateApp(context.Background(), persisted); err != nil {
				uc.log.Error("CreateApp: persist", "error", err, "app_id", persisted.ID)
			}
		},
	})
}
