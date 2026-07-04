package app

import (
	"context"
	"encoding/json"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
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

// ControlApp dispatches a lifecycle action (start/stop/restart/update) to the
// server's agent. Pure agent operation: no DB change; the result flows over SSE.
func (uc *UseCase) ControlApp(ctx context.Context, userID, serverID, appID string, action command.ControlAction) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppControl,
		Payload: command.ControlAppRequest{AppID: appID, Action: action},
	})
}

// DeleteApp dispatches an app.delete to the agent and, on success, removes the
// app from the DB cache.
func (uc *UseCase) DeleteApp(ctx context.Context, userID, serverID, appID string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppDelete,
		Payload: command.DeleteAppRequest{AppID: appID},
		OnResult: func(res port.CommandResult) {
			if !res.Success {
				return
			}
			if err := uc.repo.DeleteApp(context.Background(), appID); err != nil {
				uc.log.Error("DeleteApp: remove row", "error", err, "app_id", appID)
			}
		},
	})
}

// RenameApp dispatches an app.rename to the agent and, on success, updates the
// app's name in the DB cache.
func (uc *UseCase) RenameApp(ctx context.Context, userID, serverID, appID, name string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppRename,
		Payload: command.RenameAppRequest{AppID: appID, Name: name},
		OnResult: func(res port.CommandResult) {
			if !res.Success {
				return
			}
			if err := uc.repo.RenameApp(context.Background(), appID, name); err != nil {
				uc.log.Error("RenameApp: update row", "error", err, "app_id", appID)
			}
		},
	})
}

// GetApp dispatches an app.get to the agent (its filesystem holds the config).
// The result is delivered over SSE, correlated by request id.
func (uc *UseCase) GetApp(ctx context.Context, userID, serverID, appID string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppGet,
		Payload: command.GetAppRequest{AppID: appID},
	})
}

// GetRevisions dispatches an app.revisions to the agent: the app's git
// history, newest first. The result is delivered over SSE.
func (uc *UseCase) GetRevisions(ctx context.Context, userID, serverID, appID string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppRevisions,
		Payload: command.GetRevisionsRequest{AppID: appID},
	})
}

// RollbackApp dispatches an app.rollback: the agent restores the given commit
// as a new revision and redeploys. The result is delivered over SSE.
func (uc *UseCase) RollbackApp(ctx context.Context, userID, serverID, appID, hash string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppRollback,
		Payload: command.RollbackAppRequest{AppID: appID, Hash: hash},
	})
}

// GetLogs dispatches an app.logs to the agent. The log entries return over SSE.
func (uc *UseCase) GetLogs(ctx context.Context, userID, serverID, appID string, since int64, tail int32) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppLogs,
		Payload: command.GetLogsRequest{AppID: appID, Since: since, Tail: tail},
	})
}

// CreateApp dispatches an app.save command to the server's agent and returns
// the request id immediately. This call does not block. payload carries the
// app's config blob plus its files and variables (secrets pre-encrypted by the
// browser); app is the catalog record persisted to the DB on success. When the
// agent confirms the save, the OnResult hook persists the app and the result is
// delivered to the user over SSE.
func (uc *UseCase) CreateApp(ctx context.Context, userID, serverID string, app model.App, payload command.AppPayload) (string, error) {
	// New apps have no id yet; the API owns identity, so assign one here and use
	// it both on the wire (the agent keys its on-disk storage by app id) and on
	// the persisted catalog record.
	if app.ID == "" {
		app.ID = util.GenerateID()
	}
	payload.AppID = app.ID
	if len(payload.Config) == 0 {
		// Fall back to the catalog record as the config blob when the caller
		// didn't supply a richer config (keeps older callers working).
		cfgBytes, _ := json.Marshal(app)
		payload.Config = cfgBytes
	}
	req := command.SaveAppRequest{App: payload}
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
