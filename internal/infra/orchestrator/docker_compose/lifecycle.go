package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"winterflow/internal/domain/command"
	"winterflow/pkg/fsutil"
)

// appExists reports whether the app's data dir is present.
func (r *Repository) appExists(appID string) bool {
	_, err := os.Stat(r.appDataDir(appID))
	return err == nil
}

// deploy (re)materializes secrets and brings the app up — the folder is the
// deployment, so nothing needs rendering.
func (r *Repository) deploy(ctx context.Context, appID string) error {
	end, err := r.beginOp(appID)
	if err != nil {
		return err
	}
	defer end()
	dir := r.appDataDir(appID)
	if err := r.materializeSecrets(dir, loadSecretStore(dir)); err != nil {
		return fmt.Errorf("materialize secrets: %w", err)
	}
	if err := r.composeUp(ctx, appID); err != nil {
		return err
	}
	// The worktree (HEAD) is what just went live.
	r.markDeployed(appID, "")
	return nil
}

// StartApp brings an app up.
func (r *Repository) StartApp(ctx context.Context, appID string) error {
	end, err := r.beginOp(appID)
	if err != nil {
		return err
	}
	defer end()
	if !r.appExists(appID) {
		return fmt.Errorf("app %s not found", appID)
	}
	return r.deploy(ctx, appID)
}

// StopApp stops all containers of the app (compose down). Missing app is a
// no-op.
func (r *Repository) StopApp(ctx context.Context, appID string) error {
	if err := r.refuseDuringRestore(appID); err != nil {
		return err
	}
	return r.composeDown(ctx, appID, false)
}

// RestartApp restarts the app's containers in place.
func (r *Repository) RestartApp(ctx context.Context, appID string) error {
	end, err := r.beginOp(appID)
	if err != nil {
		return err
	}
	defer end()
	if !r.appExists(appID) {
		return fmt.Errorf("app %s not found", appID)
	}
	return r.composeRestart(ctx, appID)
}

// UpdateApp refreshes a git-sourced app's upstream, then pulls the latest
// images and recreates the app's containers.
func (r *Repository) UpdateApp(ctx context.Context, appID string) error {
	end, err := r.beginOp(appID)
	if err != nil {
		return err
	}
	defer end()
	if !r.appExists(appID) {
		return fmt.Errorf("app %s not found", appID)
	}
	if r.appSourceSpec(r.appDataDir(appID)) != nil {
		if _, _, err := r.refreshSourceWithoutDeploy(appID); err != nil {
			return fmt.Errorf("refresh source: %w", err)
		}
	}
	if err := r.composePull(ctx, appID); err != nil {
		return fmt.Errorf("docker compose pull: %w", err)
	}
	if err := r.deploy(ctx, appID); err != nil {
		return fmt.Errorf("docker compose up (after pull): %w", err)
	}
	return nil
}

// DeleteResult is the outcome of DeleteApp. Warnings are cleanup steps that
// failed (never an error); VolumesDeleted reports whether the volume sweep
// actually ran, BackupsDeleted whether the backups were deleted (not just
// marked).
type DeleteResult struct {
	Warnings       []string
	VolumesDeleted bool
	BackupsDeleted bool
}

// DeleteApp stops the app's containers and removes its folder and symlink.
// The git history goes with the folder — deletion is final. With
// deleteVolumes it also removes the app's compose-owned volumes; cleanup
// failures come back as warnings, never as an error. The sweep is skipped for
// an app this agent does not have, so a delete request cannot be used to
// destroy orphan volumes an admin deliberately kept (those go through the
// volume registry). The app's backups, in every storage its backups use,
// are deleted with deleteBackups, else marked as a deleted app's (the daily
// sweep removes them past BACKUP_RETENTION_DAYS); failures there are
// warnings too.
//
// A restore of the app that is running is waited for (it is being cancelled:
// see ErrAppDeleted), for at most restoreWaitForDelete and ctx: the delete
// never runs beside the helper swapping the app's data. If the restore is
// still running then, the delete fails with ErrRestoring and can be retried.
func (r *Repository) DeleteApp(ctx context.Context, appID string, deleteVolumes, deleteBackups bool) (DeleteResult, error) {
	if err := r.waitForRestore(ctx, appID); err != nil {
		return DeleteResult{}, err
	}
	// Decided before anything is removed: sweep only an app this agent has.
	exists := r.appExists(appID)
	res := DeleteResult{VolumesDeleted: deleteVolumes && exists, BackupsDeleted: deleteBackups && exists}
	var backupStorages []string
	appName := appID
	if exists {
		var err error
		if backupStorages, err = r.appBackupStorages(appID); err != nil {
			res.BackupsDeleted = false
			res.Warnings = append(res.Warnings, "backups: the app's backup settings can't be read, so its backup folders are left as they are: "+err.Error())
		} else {
			// Copies in a storage the app's backups no longer name are its
			// copies too: settled with the rest, instead of left as orphans.
			extra, warns := r.storagesHoldingBackups(ctx, appID, backupStorages)
			backupStorages = append(backupStorages, extra...)
			res.Warnings = append(res.Warnings, warns...)
		}
		appName = r.appName(r.appDataDir(appID), appID)
	}
	downErr := r.composeDown(ctx, appID, deleteVolumes)
	if downErr != nil {
		// Best-effort stop; continue with removal so a broken deployment can
		// still be deleted.
		r.log.Warn("delete: compose down failed, continuing", "app_id", appID, "error", downErr)
	}
	if res.VolumesDeleted {
		res.Warnings = append(res.Warnings, r.sweepAppVolumes(ctx, appID, downErr != nil)...)
	}
	if err := removeAppSymlink(r.cfg.GetAppsDir(), appID); err != nil {
		r.log.Warn("delete: symlink removal failed, continuing", "app_id", appID, "error", err)
	}
	if err := os.RemoveAll(r.appDataDir(appID)); err != nil {
		res.BackupsDeleted = false // nothing of the backups was touched
		return res, fmt.Errorf("remove app dir: %w", err)
	}
	r.forgetApp(appID)
	warnings, purged := r.settleBackups(ctx, appID, appName, backupStorages, deleteBackups)
	res.Warnings = append(res.Warnings, warnings...)
	if len(backupStorages) > 0 { // else nothing to delete: res stands as it was
		res.BackupsDeleted = deleteBackups && purged
	}
	return res, nil
}

// GetApp reconstructs the deployable payload from the app's folder. Secret
// values are masked with the "<encrypted>" placeholder (ciphertext never
// leaves the agent either); the editor sends the placeholder back on save and
// the stored ciphertext is preserved.
func (r *Repository) GetApp(ctx context.Context, appID string) (command.GetAppResponse, error) {
	var resp command.GetAppResponse
	dir := r.appDataDir(appID)
	if !r.appExists(appID) {
		return resp, fmt.Errorf("app %s not found", appID)
	}

	_, raw, err := r.readAppConfig(dir)
	if err != nil {
		return resp, fmt.Errorf("read config: %w", err)
	}
	payload := command.AppPayload{AppID: appID, Config: raw}
	encVars, encFiles := encryptedNames(raw)
	store := loadSecretStore(dir)

	// Variables: plain from .env, secrets masked.
	envValues := map[string]string{}
	if rawEnv, err := os.ReadFile(filepath.Join(dir, envRel)); err == nil {
		envValues = parseEnv(rawEnv)
	}
	for name, val := range envValues {
		if name == managedEnvVar {
			continue
		}
		payload.Variables = append(payload.Variables, command.ContentItem{Name: name, Content: []byte(val)})
	}
	for name := range store.Variables {
		if !encVars[name] {
			// The config is authoritative for what's secret; tolerate drift by
			// still masking anything present in the secret store.
			r.log.Debug("secret variable not marked in config", "name", name)
		}
		payload.Variables = append(payload.Variables, command.ContentItem{
			Name: name, Encrypted: true, Content: []byte(command.EncryptedPlaceholder),
		})
	}

	// Files: the config's file list is authoritative.
	for _, f := range configFiles(raw) {
		if encFiles[f] || store.Files[f] != "" {
			payload.Files = append(payload.Files, command.ContentItem{
				Name: f, Encrypted: true, Content: []byte(command.EncryptedPlaceholder),
			})
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			r.log.Warn("configured file missing on disk", "app_id", appID, "file", f, "error", err)
			continue
		}
		payload.Files = append(payload.Files, command.ContentItem{Name: f, Content: content})
	}

	// Git-sourced app: echo the source config with the token masked, so the
	// editor can redisplay it.
	if spec := sourceFromConfig(func() map[string]any { c, _, _ := r.readAppConfig(dir); return c }()); spec != nil {
		src := &command.SourcePayload{
			RepoURL:     spec.RepoURL,
			Branch:      spec.Branch,
			ComposePath: spec.ComposePath,
			AutoUpdate:  spec.AutoUpdate,
			PollSeconds: spec.PollSeconds,
		}
		if store.SourceToken != "" {
			src.Token = []byte(command.EncryptedPlaceholder)
		}
		payload.Source = src
	}

	resp.App = payload
	files := r.appComposeFiles(dir)
	cfg, err := r.appBackups(dir, files)
	if err != nil {
		resp.BackupsError = err.Error()
	}
	resp.Backups = cfg.backups
	if cfg.overridesOK {
		resp.BackupsBase = backupsBase(cfg.overrides)
	}
	// The WF_STORAGE_ / WF_BACKUP_ variables the compose files reference:
	// read-only rows in the editor. An unreadable compose file just shows
	// none.
	if refs, err := r.storageRefs(dir, files); err == nil {
		resp.StorageVariables = storageVariables(refs, cfg.backups)
	}
	// Where a backup added in the UI can point, and the server's ceiling
	// (the UI notes retention tiers that reach past it).
	if cfg.composeOK && !cfg.compose.Unparsed {
		t := cfg.compose.Targets()
		resp.BackupTargets = &t
	}
	resp.RetentionDays = r.cfg.GetBackupRetentionDays()
	return resp, nil
}

// configFiles lists the filenames the committed config declares.
func configFiles(cfg []byte) []string {
	var c struct {
		Files []struct {
			Filename string `json:"filename"`
		} `json:"files"`
	}
	if json.Unmarshal(cfg, &c) != nil {
		return nil
	}
	out := make([]string, 0, len(c.Files))
	for _, f := range c.Files {
		if f.Filename != "" {
			out = append(out, f.Filename)
		}
	}
	return out
}

// encryptedNames parses the stored config to find which variables/files are
// marked secret, so their values can be masked when read back.
func encryptedNames(cfg []byte) (vars map[string]bool, files map[string]bool) {
	vars = map[string]bool{}
	files = map[string]bool{}
	var c struct {
		Files []struct {
			Filename    string `json:"filename"`
			IsEncrypted bool   `json:"is_encrypted"`
		} `json:"files"`
		Variables []struct {
			Name        string `json:"name"`
			IsEncrypted bool   `json:"is_encrypted"`
		} `json:"variables"`
	}
	if json.Unmarshal(cfg, &c) != nil {
		return vars, files
	}
	for _, f := range c.Files {
		if f.IsEncrypted {
			files[f.Filename] = true
		}
	}
	for _, v := range c.Variables {
		if v.IsEncrypted {
			vars[v.Name] = true
		}
	}
	return vars, files
}

// RenameApp updates the app's display name in its committed config (a new
// commit) and swaps the symlink. The compose project name is id-based, so no
// restart is needed.
func (r *Repository) RenameApp(ctx context.Context, appID, newName string) error {
	if err := r.refuseDuringRestore(appID); err != nil {
		return err
	}
	unlock, err := r.lockApp(ctx, appID)
	if err != nil {
		return err
	}
	defer unlock()
	dir := r.appDataDir(appID)
	cfg, _, err := r.readAppConfig(dir)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	// A rename changes nothing that runs: an app deployed at the previous
	// HEAD stays deployed at the new one (it would otherwise read as having
	// undeployed changes).
	prevHead, _ := gitHead(dir)
	mark, marked := readDeployedMark(dir)
	wasDeployed := marked && mark == prevHead
	cfg["name"] = newName
	updated, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := fsutil.WriteFile(filepath.Join(dir, configRel), updated, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	hash, err := gitCommitAll(dir, "rename to "+newName)
	if err != nil {
		return fmt.Errorf("commit rename: %w", err)
	}
	if wasDeployed {
		r.markDeployed(appID, hash)
	}
	if _, err := ensureAppSymlink(r.cfg.GetAppsDir(), "apps-data", appID, newName); err != nil {
		r.log.Warn("failed to update app symlink", "app_id", appID, "error", err)
	}
	return nil
}
