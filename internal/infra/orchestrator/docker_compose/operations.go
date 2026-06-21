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

	// Resolve secrets (decrypt or preserve via "<encrypted>") against the
	// previous revision's stored plaintext, keyed by name.
	prevVars, prevFiles := r.previousValues(app.AppID)
	vars := r.resolveItems(app.Variables, prevVars)
	files := r.resolveItems(app.Files, prevFiles)

	revDir := path.Join(r.cfg.GetAppsTemplatesDir(), app.AppID, strconv.FormatUint(uint64(rev), 10))
	if err := r.writeRevision(revDir, app.Config, vars, files); err != nil {
		return 0, fmt.Errorf("write revision: %w", err)
	}

	if err := r.render(revDir, app.AppID, vars); err != nil {
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

// writeRevision stores a revision on disk: the config blob, each file under
// files/{name} (resolved plaintext), and the variable values keyed by name in
// vars/values.json. File names may contain relative subpaths.
func (r *Repository) writeRevision(revDir string, config []byte, vars, files []resolvedItem) error {
	if err := os.MkdirAll(path.Join(revDir, "files"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(revDir, "vars"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path.Join(revDir, "config.json"), config, 0o644); err != nil {
		return err
	}
	for _, f := range files {
		dst := path.Join(revDir, "files", f.name)
		if err := os.MkdirAll(path.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, f.content, 0o600); err != nil {
			return err
		}
	}
	values := make(map[string]string, len(vars))
	for _, v := range vars {
		values[v.name] = string(v.content)
	}
	valuesJSON, _ := json.Marshal(values)
	return os.WriteFile(path.Join(revDir, "vars", "values.json"), valuesJSON, 0o600)
}

// render substitutes variables into each stored template file and writes the
// result into the running directory apps/{appID}/, preserving relative paths.
func (r *Repository) render(revDir, appID string, vars []resolvedItem) error {
	values := make(map[string]string, len(vars))
	for _, v := range vars {
		values[v.name] = string(v.content)
	}

	runDir := path.Join(r.cfg.GetAppsDir(), appID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}

	filesDir := path.Join(revDir, "files")
	return filepath.WalkDir(filesDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(filesDir, p)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rendered, err := template.Substitute(string(raw), values)
		if err != nil {
			return fmt.Errorf("substitute %s: %w", rel, err)
		}
		dst := path.Join(runDir, rel)
		if err := os.MkdirAll(path.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, []byte(rendered), 0o600)
	})
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
