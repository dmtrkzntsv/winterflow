package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/pkg/template"
)

// SaveApp persists a new revision of the app, renders its templated files into
// the running directory, and brings the deployment up with `docker compose up
// -d`. It returns the assigned revision number.
func (r *Repository) SaveApp(ctx context.Context, app command.AppPayload) (uint32, error) {
	if app.AppID == "" {
		return 0, fmt.Errorf("app_id is required")
	}

	revisions, err := r.listRevisions(app.AppID)
	if err != nil {
		return 0, err
	}
	rev := nextRevision(revisions)

	revDir := path.Join(r.cfg.GetAppsTemplatesDir(), app.AppID, strconv.FormatUint(uint64(rev), 10))
	if err := r.writeRevision(revDir, app); err != nil {
		return 0, fmt.Errorf("write revision: %w", err)
	}

	if err := r.render(revDir, app); err != nil {
		return 0, fmt.Errorf("render templates: %w", err)
	}

	if err := r.composeUp(ctx, app.AppID); err != nil {
		return 0, fmt.Errorf("docker compose up: %w", err)
	}

	r.pruneRevisions(app.AppID, append(revisions, rev))
	return rev, nil
}

// GetAppsStatus reports container status for every app the agent knows about.
func (r *Repository) GetAppsStatus(ctx context.Context) ([]command.AppStatus, error) {
	entries, err := os.ReadDir(r.cfg.GetAppsTemplatesDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []command.AppStatus{}, nil
		}
		return nil, err
	}

	out := make([]command.AppStatus, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		appID := e.Name()
		containers, err := r.composePS(ctx, appID)
		if err != nil {
			r.log.Warn("failed to read app status", "app_id", appID, "error", err)
			out = append(out, command.AppStatus{AppID: appID, StatusCode: command.ContainerStatusUnknown})
			continue
		}
		cs := toContainerStatuses(containers)
		out = append(out, command.AppStatus{
			AppID:      appID,
			StatusCode: aggregateStatus(cs),
			Containers: cs,
		})
	}
	return out, nil
}

// ListApps returns the apps the agent actually has on disk (its filesystem is
// the source of truth). For each app dir it reads the latest revision's
// config.json, which holds the marshaled app info.
func (r *Repository) ListApps(ctx context.Context) ([]model.App, error) {
	entries, err := os.ReadDir(r.cfg.GetAppsTemplatesDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []model.App{}, nil
		}
		return nil, err
	}

	out := make([]model.App, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		appID := e.Name()
		revs, err := r.listRevisions(appID)
		if err != nil || len(revs) == 0 {
			continue
		}
		latest := revs[len(revs)-1]
		cfgPath := path.Join(r.cfg.GetAppsTemplatesDir(), appID, strconv.FormatUint(uint64(latest), 10), "config.json")
		raw, err := os.ReadFile(cfgPath)
		if err != nil {
			r.log.Warn("failed to read app config", "app_id", appID, "error", err)
			continue
		}
		var app model.App
		if err := json.Unmarshal(raw, &app); err != nil {
			r.log.Warn("failed to parse app config", "app_id", appID, "error", err)
			continue
		}
		app.ID = appID // dir name is authoritative
		out = append(out, app)
	}
	return out, nil
}

// --- internals ----------------------------------------------------------------

func (r *Repository) listRevisions(appID string) ([]uint32, error) {
	dir := path.Join(r.cfg.GetAppsTemplatesDir(), appID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return parseRevisions(names), nil
}

func (r *Repository) writeRevision(revDir string, app command.AppPayload) error {
	if err := os.MkdirAll(path.Join(revDir, "files"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(revDir, "vars"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path.Join(revDir, "config.json"), app.Config, 0o644); err != nil {
		return err
	}
	for _, f := range app.Files {
		if err := os.WriteFile(path.Join(revDir, "files", f.ID), f.Content, 0o600); err != nil {
			return err
		}
	}
	vars := make(map[string]string, len(app.Variables))
	for _, v := range app.Variables {
		vars[v.ID] = string(v.Content)
	}
	varsJSON, _ := json.Marshal(vars)
	return os.WriteFile(path.Join(revDir, "vars", "values.json"), varsJSON, 0o600)
}

// render substitutes variables into each template file and writes the result
// into the running directory apps/{appID}/.
func (r *Repository) render(revDir string, app command.AppPayload) error {
	vars := make(map[string]string, len(app.Variables))
	for _, v := range app.Variables {
		vars[v.ID] = string(v.Content)
	}

	runDir := path.Join(r.cfg.GetAppsDir(), app.AppID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}

	filesDir := path.Join(revDir, "files")
	entries, err := os.ReadDir(filesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(path.Join(filesDir, e.Name()))
		if err != nil {
			return err
		}
		rendered, err := template.Substitute(string(raw), vars)
		if err != nil {
			return fmt.Errorf("substitute %s: %w", e.Name(), err)
		}
		if err := os.WriteFile(path.Join(runDir, e.Name()), []byte(rendered), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) pruneRevisions(appID string, all []uint32) {
	for _, rev := range revisionsToPrune(all, maxRevisions) {
		dir := path.Join(r.cfg.GetAppsTemplatesDir(), appID, strconv.FormatUint(uint64(rev), 10))
		if err := os.RemoveAll(dir); err != nil {
			r.log.Warn("failed to prune revision", "app_id", appID, "revision", rev, "error", err)
		}
	}
}

// composeUp runs `docker compose up -d` in the app's rendered directory.
func (r *Repository) composeUp(ctx context.Context, appID string) error {
	runDir := path.Join(r.cfg.GetAppsDir(), appID)
	cmd := exec.CommandContext(ctx, "docker", "compose", "--project-name", projectName(appID), "up", "-d")
	cmd.Dir = runDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// composeDown runs `docker compose down --remove-orphans` in the app's rendered
// directory. It is a no-op (nil) if the directory does not exist.
func (r *Repository) composeDown(ctx context.Context, appID string) error {
	runDir := path.Join(r.cfg.GetAppsDir(), appID)
	if _, err := os.Stat(runDir); err != nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, "docker", "compose", "--project-name", projectName(appID), "down", "--remove-orphans")
	cmd.Dir = runDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// composeRestart runs `docker compose restart` in the app's rendered directory.
func (r *Repository) composeRestart(ctx context.Context, appID string) error {
	runDir := path.Join(r.cfg.GetAppsDir(), appID)
	cmd := exec.CommandContext(ctx, "docker", "compose", "--project-name", projectName(appID), "restart")
	cmd.Dir = runDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// composePull runs `docker compose pull` in the app's rendered directory.
func (r *Repository) composePull(ctx context.Context, appID string) error {
	runDir := path.Join(r.cfg.GetAppsDir(), appID)
	cmd := exec.CommandContext(ctx, "docker", "compose", "--project-name", projectName(appID), "pull")
	cmd.Dir = runDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// composePS runs `docker compose ps --format json` and parses the per-container
// lines.
func (r *Repository) composePS(ctx context.Context, appID string) ([]composePS, error) {
	runDir := path.Join(r.cfg.GetAppsDir(), appID)
	if _, err := os.Stat(runDir); err != nil {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, "docker", "compose", "--project-name", projectName(appID), "ps", "--format", "json", "--all")
	cmd.Dir = runDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseComposePS(out)
}

// parseComposePS handles both the newline-delimited JSON objects (newer compose)
// and the single JSON array forms of `docker compose ps --format json`.
func parseComposePS(out []byte) ([]composePS, error) {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var arr []composePS
		if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
			return nil, err
		}
		return arr, nil
	}
	var lines []composePS
	for _, l := range strings.Split(trimmed, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		var c composePS
		if err := json.Unmarshal([]byte(l), &c); err != nil {
			return nil, err
		}
		lines = append(lines, c)
	}
	return lines, nil
}

// projectName derives a stable docker compose project name from an app id.
func projectName(appID string) string {
	return "wf-" + filepath.Base(appID)
}
