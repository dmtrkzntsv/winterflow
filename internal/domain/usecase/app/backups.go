package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
)

// backupRunWait is how long the API awaits a Back up now result: the
// agent's own bound (command.BackupRunWait; the run may queue behind others
// and runs for up to 30 minutes) plus slack for the trip, so the agent's
// timeout answer is never dropped as late.
const backupRunWait = command.BackupRunWait + 5*time.Minute

// checkBackups validates the backups a save makes when its compose file is
// in the request: the recipe, the request's backups list when its config
// carries one (an enabled backup's storage must be on the server), and
// returns which backup names WF_BACKUP_ references may use. The app editor
// doesn't send the backups list (the agent keeps the stored one), so for an
// edit without it a name outside the recipe is left to the agent.
func (uc *UseCase) checkBackups(ctx context.Context, serverID string, contents [][]byte, config []byte, isNew bool) (func(string) bool, error) {
	comp, err := recipe.ParseCompose(contents...)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Backups *[]recipe.BackupConfig `json:"backups"`
	}
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, fmt.Errorf("%w: backups: %v", recipe.ErrInvalid, err)
		}
	}
	var overrides []recipe.BackupConfig
	if cfg.Backups != nil {
		overrides = *cfg.Backups
	}
	backups, err := recipe.Effective(comp, overrides)
	if err != nil {
		return nil, err
	}
	if err := uc.requireBackupStorages(ctx, serverID, backups); err != nil {
		return nil, err
	}
	declared := recipe.Declared(backups)
	known := cfg.Backups != nil || isNew
	return func(name string) bool {
		_, ok := declared[name]
		return ok || !known
	}, nil
}

// requireBackupStorages refuses an enabled backup whose storage isn't in
// the server's storage index (without the index, the agent's check is the
// only one).
func (uc *UseCase) requireBackupStorages(ctx context.Context, serverID string, backups []recipe.EffectiveBackup) error {
	if uc.serverStorages == nil {
		return nil
	}
	var have map[string]bool
	for _, b := range backups {
		if !b.Enabled {
			continue
		}
		if have == nil {
			list, err := uc.serverStorages.ListForServer(ctx, serverID)
			if err != nil {
				uc.log.Error("backups: list storages", "error", err, "server_id", serverID)
				return err
			}
			have = map[string]bool{}
			for _, s := range list {
				have[s.Name] = true
			}
		}
		if !have[b.Storage] {
			return fmt.Errorf("%w: backup %s: there is no storage %q on this server", recipe.ErrInvalid, b.Name, b.Storage)
		}
	}
	return nil
}

// UpdateBackups dispatches backup.update (callers validate req first): the
// app's backups list replaced as a whole. An enabled backup's storage must
// be in the server's index (400); the agent checks the list against the
// compose file's recipe and the app's mounts.
func (uc *UseCase) UpdateBackups(ctx context.Context, userID, serverID string, req command.UpdateBackupsRequest) (string, error) {
	var enabled []recipe.EffectiveBackup
	for _, b := range req.Backups {
		if b.Enabled {
			enabled = append(enabled, recipe.EffectiveBackup{Name: b.Name, Enabled: true, Storage: b.Storage})
		}
	}
	if err := uc.requireBackupStorages(ctx, serverID, enabled); err != nil {
		return "", err
	}
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, UserID: userID, Type: command.TypeBackupUpdate, Payload: req,
	})
}

// ListRestorePoints dispatches backup.points.
func (uc *UseCase) ListRestorePoints(ctx context.Context, userID, serverID string, req command.ListRestorePointsRequest) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, UserID: userID, Type: command.TypeBackupPoints, Payload: req,
	})
}

// RunBackup dispatches backup.run; its result arrives when the run ends.
func (uc *UseCase) RunBackup(ctx context.Context, userID, serverID string, req command.RunBackupRequest) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, UserID: userID, Type: command.TypeBackupRun, Payload: req, Wait: backupRunWait,
	})
}

// DeleteRestorePoint dispatches backup.delete-point.
func (uc *UseCase) DeleteRestorePoint(ctx context.Context, userID, serverID string, req command.DeleteRestorePointRequest) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, UserID: userID, Type: command.TypeBackupDeletePoint, Payload: req,
	})
}

// RestoreBackup dispatches backup.restore; its result arrives when the
// restore ends (it runs on the backup scheduler, after any run ahead of it).
func (uc *UseCase) RestoreBackup(ctx context.Context, userID, serverID string, req command.RestoreBackupRequest) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, UserID: userID, Type: command.TypeBackupRestore, Payload: req, Wait: backupRunWait,
	})
}

// ListOrphans dispatches storage.orphans.
func (uc *UseCase) ListOrphans(ctx context.Context, userID, serverID string, req command.ListOrphansRequest) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, UserID: userID, Type: command.TypeStorageOrphans, Payload: req,
	})
}

// DeleteOrphan dispatches storage.delete-orphan. The agent only knows its own
// server's apps; the API's catalog knows the others', so a folder of an app
// that is live on another server (the usual way storages are shared) is
// refused here: its backups are not orphans.
func (uc *UseCase) DeleteOrphan(ctx context.Context, userID, serverID string, req command.DeleteOrphanRequest) (string, error) {
	switch app, err := uc.repo.GetApp(ctx, req.AppID); {
	case errors.Is(err, model.ErrAppNotFound):
	case err != nil:
		return "", err
	case app.ID != "" && app.ServerID != serverID:
		return "", model.Rejected("that app is live on another server: its backups are not orphans, and are deleted with the app")
	}
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, UserID: userID, Type: command.TypeStorageDeleteOrphan, Payload: req,
	})
}
