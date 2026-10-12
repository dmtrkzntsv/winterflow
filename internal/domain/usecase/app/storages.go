package app

import (
	"context"
	"encoding/json"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
)

// requireStorageRefs refuses a save whose compose file (in the request)
// declares invalid backups, or references a WF_STORAGE_ variable of a
// storage the server doesn't have or a WF_BACKUP_ variable of a backup the
// app doesn't declare. The compose files are picked the way the agent picks
// them (model.DetectComposeFiles). A git-sourced app's compose file is not
// in the request: the agent checks it at save and deploy. Without the
// storage index (old wiring) the agent's storage check is the only one.
func (uc *UseCase) requireStorageRefs(ctx context.Context, serverID string, p command.AppPayload, isNew bool) error {
	if p.Source != nil {
		return nil
	}
	files := map[string][]byte{}
	for _, f := range p.Files {
		if !f.Encrypted {
			files[f.ItemName()] = f.Content
		}
	}
	var contents [][]byte
	for _, name := range model.DetectComposeFiles(func(n string) bool { _, ok := files[n]; return ok }) {
		contents = append(contents, files[name])
	}
	backupDeclared, err := uc.checkBackups(ctx, serverID, contents, p.Config, isNew)
	if err != nil {
		return err
	}
	refs := model.ScanStorageRefs(contents...)
	if len(refs) == 0 {
		return nil
	}
	have := map[string]bool{}
	if uc.serverStorages != nil {
		list, err := uc.serverStorages.ListForServer(ctx, serverID)
		if err != nil {
			uc.log.Error("SaveApp: list storages", "error", err, "server_id", serverID)
			return err
		}
		for _, s := range list {
			have[s.Name] = true
		}
	}
	_, err = model.CheckStorageRefs(refs, func(name string) bool { return uc.serverStorages == nil || have[name] }, backupDeclared)
	return err
}

// ServerStorages reads the server's storages from the server_storages index
// (no agent round-trip).
func (uc *UseCase) ServerStorages(ctx context.Context, serverID string) ([]model.StorageSummary, error) {
	if uc.serverStorages == nil {
		return []model.StorageSummary{}, nil
	}
	return uc.serverStorages.ListForServer(ctx, serverID)
}

// ListServerStorages dispatches storage.list to the server's agent. The
// result reconciles the server_storages index and is delivered over SSE to
// userID. The system's own sync when an agent comes online (no user) only
// needs the registry, so the agent skips finding each storage's apps.
func (uc *UseCase) ListServerStorages(ctx context.Context, userID, serverID string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeStorageList,
		Payload: command.ListStoragesRequest{RegistryOnly: userID == ""},
		OnResult: func(res port.CommandResult) {
			if !res.Success || len(res.Payload) == 0 || uc.serverStorages == nil {
				return
			}
			var listed command.ListStoragesResponse
			if err := json.Unmarshal(res.Payload, &listed); err != nil {
				uc.log.Error("ListServerStorages: decode result", "error", err)
				return
			}
			rows := make([]model.StorageSummary, 0, len(listed.Storages))
			for _, s := range listed.Storages {
				rows = append(rows, s.Summary())
			}
			if err := uc.serverStorages.ReplaceForServer(context.Background(), serverID, rows); err != nil {
				uc.log.Error("ListServerStorages: sync index", "error", err, "server_id", serverID)
			}
		},
	})
}

// TestStorage dispatches storage.test (callers validate req first). Nothing
// is indexed: a test stores nothing.
func (uc *UseCase) TestStorage(ctx context.Context, userID, serverID string, req command.TestStorageRequest) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeStorageTest,
		Payload: req,
	})
}

// SaveStorage dispatches storage.save (callers validate req first). On the
// agent's confirmation the storage enters the index.
func (uc *UseCase) SaveStorage(ctx context.Context, userID, serverID string, req command.SaveStorageRequest) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeStorageSave,
		Payload: req,
		OnResult: func(res port.CommandResult) {
			if !res.Success || len(res.Payload) == 0 || uc.serverStorages == nil {
				return
			}
			var saved command.SaveStorageResponse
			if err := json.Unmarshal(res.Payload, &saved); err != nil {
				uc.log.Error("SaveStorage: decode result", "error", err)
				return
			}
			if err := uc.serverStorages.Upsert(context.Background(), serverID, saved.Storage.Summary()); err != nil {
				uc.log.Error("SaveStorage: update index", "error", err, "server_id", serverID)
			}
		},
	})
}

// DeleteStorage dispatches storage.delete. On the agent's confirmation the
// storage leaves the index; a refusal (in use) changes nothing.
func (uc *UseCase) DeleteStorage(ctx context.Context, userID, serverID, name string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeStorageDelete,
		Payload: command.DeleteStorageRequest{Name: name},
		OnResult: func(res port.CommandResult) {
			if !res.Success || uc.serverStorages == nil {
				return
			}
			if err := uc.serverStorages.Delete(context.Background(), serverID, name); err != nil {
				uc.log.Error("DeleteStorage: update index", "error", err, "server_id", serverID)
			}
		},
	})
}
