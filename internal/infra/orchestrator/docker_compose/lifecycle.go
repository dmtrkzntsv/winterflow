package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"winterflow/internal/domain/command"
)

// appExists reports whether the app's data dir is present.
func (r *Repository) appExists(appID string) bool {
	_, err := os.Stat(r.appDataDir(appID))
	return err == nil
}

// deploy (re)materializes secrets and brings the app up — the folder is the
// deployment, so nothing needs rendering.
func (r *Repository) deploy(ctx context.Context, appID string) error {
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
	if !r.appExists(appID) {
		return fmt.Errorf("app %s not found", appID)
	}
	return r.deploy(ctx, appID)
}

// StopApp stops all containers of the app (compose down). Missing app is a
// no-op.
func (r *Repository) StopApp(ctx context.Context, appID string) error {
	return r.composeDown(ctx, appID)
}

// RestartApp restarts the app's containers in place.
func (r *Repository) RestartApp(ctx context.Context, appID string) error {
	if !r.appExists(appID) {
		return fmt.Errorf("app %s not found", appID)
	}
	return r.composeRestart(ctx, appID)
}

// UpdateApp refreshes a git-sourced app's upstream, then pulls the latest
// images and recreates the app's containers.
func (r *Repository) UpdateApp(ctx context.Context, appID string) error {
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

// DeleteApp stops the app's containers and removes its folder and symlink.
// The git history goes with the folder — deletion is final.
func (r *Repository) DeleteApp(ctx context.Context, appID string) error {
	if err := r.composeDown(ctx, appID); err != nil {
		// Best-effort stop; continue with removal so a broken deployment can
		// still be deleted.
		r.log.Warn("delete: compose down failed, continuing", "app_id", appID, "error", err)
	}
	if err := removeAppSymlink(r.cfg.GetAppsDir(), appID); err != nil {
		r.log.Warn("delete: symlink removal failed, continuing", "app_id", appID, "error", err)
	}
	if err := os.RemoveAll(r.appDataDir(appID)); err != nil {
		return fmt.Errorf("remove app dir: %w", err)
	}
	r.forgetApp(appID)
	return nil
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
	dir := r.appDataDir(appID)
	cfg, _, err := r.readAppConfig(dir)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	cfg["name"] = newName
	updated, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, configRel), updated, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if _, err := gitCommitAll(dir, "rename to "+newName); err != nil {
		return fmt.Errorf("commit rename: %w", err)
	}
	if _, err := ensureAppSymlink(r.cfg.GetAppsDir(), "apps-data", appID, newName); err != nil {
		r.log.Warn("failed to update app symlink", "app_id", appID, "error", err)
	}
	return nil
}
