package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// SaveApp persists an app into its git-backed folder and brings the
// deployment up. The folder IS the deployment: compose runs in it directly.
// Every save is a commit; the commit hash is returned as the revision.
func (r *Repository) SaveApp(ctx context.Context, app command.AppPayload) (string, error) {
	hash, err := r.saveWithoutDeploy(app)
	if err != nil {
		return "", err
	}
	if err := r.composeUp(ctx, app.AppID); err != nil {
		return "", fmt.Errorf("docker compose up: %w", err)
	}
	return hash, nil
}

// saveWithoutDeploy is SaveApp minus the compose invocation: write the store,
// commit, materialize secrets, and fix the symlink. Split out so the full
// persistence path is testable without Docker.
func (r *Repository) saveWithoutDeploy(app command.AppPayload) (string, error) {
	if app.AppID == "" {
		return "", fmt.Errorf("app_id is required")
	}
	dir := r.appDataDir(app.AppID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := gitEnsure(dir); err != nil {
		return "", fmt.Errorf("init app repo: %w", err)
	}

	store, err := r.writeAppStore(dir, app)
	if err != nil {
		return "", fmt.Errorf("write app store: %w", err)
	}

	// Git-sourced app: sync the upstream and pin its head BEFORE committing,
	// so the lock lands inside this save's commit.
	if spec := r.appSourceSpec(dir); spec != nil {
		if _, err := r.ensureSource(context.Background(), dir, *spec, r.sourceTokenPlaintext(dir), ""); err != nil {
			return "", fmt.Errorf("sync source: %w", err)
		}
	}

	name := r.appName(dir, app.AppID)
	hash, err := gitCommitAll(dir, "save "+name)
	if err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}

	if err := r.materializeSecrets(dir, store); err != nil {
		return "", fmt.Errorf("materialize secrets: %w", err)
	}
	if _, err := ensureAppSymlink(r.cfg.GetAppsDir(), "apps-data", app.AppID, name); err != nil {
		r.log.Warn("failed to update app symlink", "app_id", app.AppID, "error", err)
	}
	return hash, nil
}

// Revisions returns the app's git history (newest first) and the current HEAD.
func (r *Repository) Revisions(ctx context.Context, appID string) ([]command.RevisionInfo, string, error) {
	dir := r.appDataDir(appID)
	if _, err := os.Stat(dir); err != nil {
		return nil, "", fmt.Errorf("app %s not found", appID)
	}
	log, err := gitLog(dir)
	if err != nil {
		return nil, "", err
	}
	out := make([]command.RevisionInfo, 0, len(log))
	for _, c := range log {
		out = append(out, command.RevisionInfo{Hash: c.Hash, Subject: c.Subject, Timestamp: c.Timestamp})
	}
	current := ""
	if len(log) > 0 {
		current = log[0].Hash
	}
	return out, current, nil
}

// Rollback restores the given commit's tree as a NEW commit (history stays
// linear), re-materializes secrets from the restored store, and redeploys.
// Returns the new HEAD hash.
func (r *Repository) Rollback(ctx context.Context, appID, hash string) (string, error) {
	newHead, err := r.rollbackWithoutDeploy(appID, hash)
	if err != nil {
		return "", err
	}
	if err := r.composeUp(ctx, appID); err != nil {
		return "", fmt.Errorf("docker compose up: %w", err)
	}
	return newHead, nil
}

// rollbackWithoutDeploy restores the commit, commits the restored tree, and
// re-materializes secrets — everything except bringing containers up.
func (r *Repository) rollbackWithoutDeploy(appID, hash string) (string, error) {
	dir := r.appDataDir(appID)
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("app %s not found", appID)
	}
	if err := gitRestore(dir, hash); err != nil {
		return "", fmt.Errorf("restore %s: %w", hash, err)
	}
	// Git-sourced app: the restored lock names the SHA that was deployed at
	// that revision — put source/ back exactly there.
	if spec := r.appSourceSpec(dir); spec != nil {
		if lock, ok := readSourceLock(dir); ok {
			if _, err := r.ensureSource(context.Background(), dir, *spec, r.sourceTokenPlaintext(dir), lock.SHA); err != nil {
				return "", fmt.Errorf("restore source: %w", err)
			}
		}
	}
	short := hash
	if len(short) > 8 {
		short = short[:8]
	}
	newHead, err := gitCommitAll(dir, "rollback to "+short)
	if err != nil {
		return "", fmt.Errorf("commit rollback: %w", err)
	}
	if err := r.materializeSecrets(dir, loadSecretStore(dir)); err != nil {
		return "", fmt.Errorf("materialize secrets: %w", err)
	}
	// The restored config may carry a different name — keep the symlink true.
	if _, err := ensureAppSymlink(r.cfg.GetAppsDir(), "apps-data", appID, r.appName(dir, appID)); err != nil {
		r.log.Warn("failed to update app symlink", "app_id", appID, "error", err)
	}
	return newHead, nil
}

// GetAppsStatus reports container status for every app the agent has.
func (r *Repository) GetAppsStatus(ctx context.Context) ([]command.AppStatus, error) {
	entries, err := os.ReadDir(r.cfg.GetAppsDataDir())
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

// ListApps returns the apps present on disk (the agent's filesystem is the
// source of truth) and heals the human-readable symlinks in apps/ as a side
// effect of every listing. Version is the app's commit count.
func (r *Repository) ListApps(ctx context.Context) ([]model.App, error) {
	entries, err := os.ReadDir(r.cfg.GetAppsDataDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []model.App{}, nil
		}
		return nil, err
	}

	names := map[string]string{}
	out := make([]model.App, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		appID := e.Name()
		dir := filepath.Join(r.cfg.GetAppsDataDir(), appID)
		_, raw, err := r.readAppConfig(dir)
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
		if n, err := gitCount(dir); err == nil {
			app.Version = strconv.Itoa(n)
		}
		out = append(out, app)
		names[appID] = app.Name
	}

	if err := healAppSymlinks(r.cfg.GetAppsDir(), "apps-data", names); err != nil {
		r.log.Warn("failed to heal app symlinks", "error", err)
	}
	return out, nil
}

// appSourceSpec reads the app's source configuration from the committed
// config blob; nil for editor-authored apps.
func (r *Repository) appSourceSpec(dir string) *sourceSpec {
	cfg, _, err := r.readAppConfig(dir)
	if err != nil {
		return nil
	}
	return sourceFromConfig(cfg)
}

// refreshSourceWithoutDeploy fetches the upstream and, when its head moved,
// re-pins and commits. Returns whether anything changed and the new SHA.
func (r *Repository) refreshSourceWithoutDeploy(appID string) (bool, string, error) {
	dir := r.appDataDir(appID)
	spec := r.appSourceSpec(dir)
	if spec == nil {
		return false, "", fmt.Errorf("app %s is not git-sourced", appID)
	}
	prev, _ := readSourceLock(dir)
	sha, err := r.ensureSource(context.Background(), dir, *spec, r.sourceTokenPlaintext(dir), "")
	if err != nil {
		return false, "", err
	}
	if sha == prev.SHA {
		return false, sha, nil
	}
	short := sha
	if len(short) > 8 {
		short = short[:8]
	}
	if _, err := gitCommitAll(dir, "update source to "+short); err != nil {
		return false, "", fmt.Errorf("commit source update: %w", err)
	}
	return true, sha, nil
}

// defaultPollSeconds is the upstream poll interval when an app doesn't set
// its own.
const defaultPollSeconds = 120

// RefreshDueSources polls every git-sourced app whose auto_update is on and
// whose interval has elapsed; on an upstream advance it re-pins (a commit)
// and redeploys. Returns the ids that were updated. Redeploy failures are
// logged, not fatal — the pin already moved and the next control action or
// poll retries the deploy.
func (r *Repository) RefreshDueSources(ctx context.Context) []string {
	entries, err := os.ReadDir(r.cfg.GetAppsDataDir())
	if err != nil {
		return nil
	}
	var updated []string
	now := time.Now()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		appID := e.Name()
		spec := r.appSourceSpec(filepath.Join(r.cfg.GetAppsDataDir(), appID))
		if spec == nil || !spec.AutoUpdate {
			continue
		}
		interval := time.Duration(spec.PollSeconds) * time.Second
		if interval <= 0 {
			interval = defaultPollSeconds * time.Second
		}
		r.sourceMu.Lock()
		last := r.sourceChecks[appID]
		due := now.Sub(last) >= interval
		if due {
			r.sourceChecks[appID] = now
		}
		r.sourceMu.Unlock()
		if !due {
			continue
		}

		changed, sha, err := r.refreshSourceWithoutDeploy(appID)
		if err != nil {
			r.log.Warn("source poll failed", "app_id", appID, "error", err)
			continue
		}
		if !changed {
			continue
		}
		r.log.Info("source updated, redeploying", "app_id", appID, "sha", sha)
		if err := r.deploy(ctx, appID); err != nil {
			r.log.Warn("redeploy after source update failed", "app_id", appID, "error", err)
		}
		updated = append(updated, appID)
	}
	return updated
}

// appName reads the display name from the committed config, falling back to
// the app id.
func (r *Repository) appName(dir, appID string) string {
	cfg, _, err := r.readAppConfig(dir)
	if err != nil {
		return appID
	}
	if n, _ := cfg["name"].(string); n != "" {
		return n
	}
	return appID
}

// --- compose helpers ------------------------------------------------------

// composeFile resolves the compose file for git-sourced apps: the configured
// compose_path inside source/, else a repo-root compose file, else "" (auto-
// detect in the app dir — the winterflow-authored root compose). Non-source
// apps always auto-detect.
func (r *Repository) composeFile(dir string, spec *sourceSpec) string {
	if spec == nil {
		return ""
	}
	if spec.ComposePath != "" {
		if rel, err := safeRel(spec.ComposePath); err == nil {
			return filepath.Join(sourceDirRel, rel)
		}
		return ""
	}
	for _, candidate := range []string{"compose.yml", "compose.yaml", "docker-compose.yml", "docker-compose.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, sourceDirRel, candidate)); err == nil {
			return filepath.Join(sourceDirRel, candidate)
		}
	}
	return ""
}

// composeArgs builds the common CLI prefix. Env files are always explicit:
// with -f pointing into source/, compose's implicit project directory moves
// there and would stop auto-loading the app's committed .env.
func (r *Repository) composeArgs(appID string, verb ...string) []string {
	args := []string{"compose", "--project-name", projectName(appID)}
	dir := r.appDataDir(appID)
	if f := r.composeFile(dir, r.appSourceSpec(dir)); f != "" {
		args = append(args, "-f", f)
	}
	args = append(args, "--env-file", envRel)
	if _, err := os.Stat(filepath.Join(dir, envSecretsRel)); err == nil {
		args = append(args, "--env-file", envSecretsRel)
	}
	return append(args, verb...)
}

func (r *Repository) composeRun(ctx context.Context, appID string, verb ...string) error {
	cmd := exec.CommandContext(ctx, "docker", r.composeArgs(appID, verb...)...)
	cmd.Dir = r.appDataDir(appID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (r *Repository) composeUp(ctx context.Context, appID string) error {
	return r.composeRun(ctx, appID, "up", "-d", "--remove-orphans")
}

// composeDown is a no-op (nil) if the app dir does not exist.
func (r *Repository) composeDown(ctx context.Context, appID string) error {
	if _, err := os.Stat(r.appDataDir(appID)); err != nil {
		return nil
	}
	return r.composeRun(ctx, appID, "down", "--remove-orphans")
}

func (r *Repository) composeRestart(ctx context.Context, appID string) error {
	return r.composeRun(ctx, appID, "restart")
}

func (r *Repository) composePull(ctx context.Context, appID string) error {
	return r.composeRun(ctx, appID, "pull")
}

// composePS runs `docker compose ps --format json` and parses the
// per-container lines.
func (r *Repository) composePS(ctx context.Context, appID string) ([]composePS, error) {
	dir := r.appDataDir(appID)
	if _, err := os.Stat(dir); err != nil {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, "docker", r.composeArgs(appID, "ps", "--format", "json", "--all")...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseComposePS(out)
}

// parseComposePS handles both the newline-delimited JSON objects (newer
// compose) and the single JSON array forms of `docker compose ps`.
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
