package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
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
func (r *Repository) readRevision(revDir, appID string) (command.AppPayload, error) {
	payload := command.AppPayload{AppID: appID}

	cfg, err := os.ReadFile(path.Join(revDir, "config.json"))
	if err != nil {
		return payload, fmt.Errorf("read config: %w", err)
	}
	payload.Config = cfg

	// Variables are stored as a single id->value JSON map.
	if raw, err := os.ReadFile(path.Join(revDir, "vars", "values.json")); err == nil {
		var vars map[string]string
		if err := json.Unmarshal(raw, &vars); err == nil {
			for id, val := range vars {
				payload.Variables = append(payload.Variables, command.ContentItem{ID: id, Content: []byte(val)})
			}
		}
	}

	// Files are stored one per id under files/.
	filesDir := path.Join(revDir, "files")
	if entries, err := os.ReadDir(filesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			content, err := os.ReadFile(path.Join(filesDir, e.Name()))
			if err != nil {
				return payload, fmt.Errorf("read file %s: %w", e.Name(), err)
			}
			payload.Files = append(payload.Files, command.ContentItem{ID: e.Name(), Content: content})
		}
	}

	return payload, nil
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
// stored revisions.
func (r *Repository) redeployLatest(ctx context.Context, appID string) error {
	resp, err := r.GetApp(ctx, appID, 0)
	if err != nil {
		return err
	}
	revDir := path.Join(r.cfg.GetAppsTemplatesDir(), appID, strconv.FormatUint(uint64(resp.Revision), 10))
	if err := r.render(revDir, resp.App); err != nil {
		return fmt.Errorf("render templates: %w", err)
	}
	return r.composeUp(ctx, appID)
}
