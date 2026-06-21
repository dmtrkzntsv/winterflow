package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"

	"winterflow/internal/domain/command"
)

// appRunDirExists reports whether the app's rendered (running) directory exists.
func (r *Repository) appRunDirExists(appID string) bool {
	if _, err := os.Stat(path.Join(r.cfg.GetAppsDir(), appID)); err != nil {
		return false
	}
	return true
}

// StartApp brings an app up. If it has never been rendered, it renders the
// latest revision first (a full deploy); otherwise it just brings the existing
// project up.
func (r *Repository) StartApp(ctx context.Context, appID string) error {
	if !r.appRunDirExists(appID) {
		return r.redeployLatest(ctx, appID)
	}
	return r.composeUp(ctx, appID)
}

// StopApp stops all containers of the app (compose down). Missing app is a
// no-op.
func (r *Repository) StopApp(ctx context.Context, appID string) error {
	return r.composeDown(ctx, appID)
}

// RestartApp restarts the app's containers in place, falling back to a full
// deploy if it was never rendered.
func (r *Repository) RestartApp(ctx context.Context, appID string) error {
	if !r.appRunDirExists(appID) {
		return r.redeployLatest(ctx, appID)
	}
	return r.composeRestart(ctx, appID)
}

// UpdateApp pulls the latest images and recreates the app's containers.
func (r *Repository) UpdateApp(ctx context.Context, appID string) error {
	if !r.appRunDirExists(appID) {
		return r.redeployLatest(ctx, appID)
	}
	if err := r.composePull(ctx, appID); err != nil {
		return fmt.Errorf("docker compose pull: %w", err)
	}
	if err := r.composeUp(ctx, appID); err != nil {
		return fmt.Errorf("docker compose up (after pull): %w", err)
	}
	return nil
}

// DeleteApp stops the app's containers and removes both its rendered run
// directory and all of its stored revisions.
func (r *Repository) DeleteApp(ctx context.Context, appID string) error {
	if err := r.composeDown(ctx, appID); err != nil {
		// Best-effort stop; continue with removal so a broken deployment can
		// still be deleted.
		r.log.Warn("delete: compose down failed, continuing", "app_id", appID, "error", err)
	}
	if err := os.RemoveAll(path.Join(r.cfg.GetAppsDir(), appID)); err != nil {
		return fmt.Errorf("remove run dir: %w", err)
	}
	if err := os.RemoveAll(path.Join(r.cfg.GetAppsTemplatesDir(), appID)); err != nil {
		return fmt.Errorf("remove templates dir: %w", err)
	}
	return nil
}

// GetApp returns the deployable payload of an app at a given revision (0 =
// latest) together with the revision used and the full list of available
// revisions.
func (r *Repository) GetApp(ctx context.Context, appID string, revision uint32) (command.GetAppResponse, error) {
	var resp command.GetAppResponse

	revisions, err := r.listRevisions(appID)
	if err != nil {
		return resp, err
	}
	if len(revisions) == 0 {
		return resp, fmt.Errorf("app %s has no revisions", appID)
	}
	resp.AvailableRevisions = revisions

	rev := revision
	if rev == 0 {
		rev = revisions[len(revisions)-1]
	}
	revDir := path.Join(r.cfg.GetAppsTemplatesDir(), appID, strconv.FormatUint(uint64(rev), 10))
	if _, err := os.Stat(revDir); err != nil {
		return resp, fmt.Errorf("revision %d not found for app %s", rev, appID)
	}

	payload, err := r.readRevision(revDir, appID)
	if err != nil {
		return resp, err
	}
	resp.App = payload
	resp.Revision = rev
	return resp, nil
}

// readRevision reconstructs an AppPayload from a stored revision directory.
// Secret values are masked with the "<encrypted>" placeholder (their plaintext
// never leaves the agent); the editor sends the placeholder back on save and the
// stored value is preserved.
func (r *Repository) readRevision(revDir, appID string) (command.AppPayload, error) {
	payload := command.AppPayload{AppID: appID}

	cfg, err := os.ReadFile(path.Join(revDir, "config.json"))
	if err != nil {
		return payload, fmt.Errorf("read config: %w", err)
	}
	payload.Config = cfg

	encVars, encFiles := encryptedNames(cfg)

	// Variables are stored as a single name->value JSON map.
	if raw, err := os.ReadFile(path.Join(revDir, "vars", "values.json")); err == nil {
		var vars map[string]string
		if err := json.Unmarshal(raw, &vars); err == nil {
			for name, val := range vars {
				item := command.ContentItem{Name: name, Content: []byte(val)}
				if encVars[name] {
					item.Encrypted = true
					item.Content = []byte(command.EncryptedPlaceholder)
				}
				payload.Variables = append(payload.Variables, item)
			}
		}
	}

	// Files are stored under files/{name} (names may contain subpaths).
	filesDir := path.Join(revDir, "files")
	_ = filepath.WalkDir(filesDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(filesDir, p)
		if relErr != nil {
			return nil
		}
		content, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		item := command.ContentItem{Name: rel, Content: content}
		if encFiles[rel] {
			item.Encrypted = true
			item.Content = []byte(command.EncryptedPlaceholder)
		}
		payload.Files = append(payload.Files, item)
		return nil
	})

	return payload, nil
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

// RenameApp updates the app's display name in its latest revision config. The
// name is catalog metadata (stored in config.json as model.App.name); it does
// not change the compose project, so no re-render or restart is required.
func (r *Repository) RenameApp(ctx context.Context, appID, newName string) error {
	revisions, err := r.listRevisions(appID)
	if err != nil {
		return err
	}
	if len(revisions) == 0 {
		return fmt.Errorf("app %s has no revisions", appID)
	}
	latest := revisions[len(revisions)-1]
	cfgPath := path.Join(r.cfg.GetAppsTemplatesDir(), appID, strconv.FormatUint(uint64(latest), 10), "config.json")

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	cfg["name"] = newName
	updated, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(cfgPath, updated, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// redeployLatest renders the latest stored revision and brings the app up. Used
// when a control action targets an app that was pruned from disk but still has
// stored revisions. The stored revision already holds resolved (plaintext)
// values, so it renders without any decryption.
func (r *Repository) redeployLatest(ctx context.Context, appID string) error {
	revisions, err := r.listRevisions(appID)
	if err != nil {
		return err
	}
	if len(revisions) == 0 {
		return fmt.Errorf("app %s has no revisions", appID)
	}
	latest := revisions[len(revisions)-1]
	revDir := path.Join(r.cfg.GetAppsTemplatesDir(), appID, strconv.FormatUint(uint64(latest), 10))

	var vars []resolvedItem
	if raw, err := os.ReadFile(path.Join(revDir, "vars", "values.json")); err == nil {
		var m map[string]string
		if json.Unmarshal(raw, &m) == nil {
			for name, val := range m {
				vars = append(vars, resolvedItem{name: name, content: []byte(val)})
			}
		}
	}

	if err := r.render(revDir, appID, vars); err != nil {
		return fmt.Errorf("render templates: %w", err)
	}
	return r.composeUp(ctx, appID)
}
