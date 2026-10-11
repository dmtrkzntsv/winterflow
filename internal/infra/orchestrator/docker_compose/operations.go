package dockercompose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	hash, _, err := r.SaveAppResult(ctx, app, false)
	return hash, err
}

// SaveAppDraft persists and commits without deploying: the commit becomes a
// draft sitting ahead of the deployed mark until the user deploys it.
func (r *Repository) SaveAppDraft(ctx context.Context, app command.AppPayload) (string, error) {
	hash, _, err := r.SaveAppResult(ctx, app, true)
	return hash, err
}

// SaveAppResult is SaveApp (or, draft, SaveAppDraft) with the save's
// warnings.
func (r *Repository) SaveAppResult(ctx context.Context, app command.AppPayload, draft bool) (string, []string, error) {
	hash, warnings, err := r.save(app)
	if err != nil || draft {
		return hash, warnings, err
	}
	if err := r.composeUp(ctx, app.AppID); err != nil {
		return "", warnings, fmt.Errorf("docker compose up: %w", err)
	}
	r.markDeployed(app.AppID, hash)
	return hash, warnings, nil
}

// DeployedRevision returns the last successfully deployed commit ("" when
// unknown — the app predates the mark or has never been deployed).
func (r *Repository) DeployedRevision(appID string) string {
	sha, _ := readDeployedMark(r.appDataDir(appID))
	return sha
}

// markDeployed records a successful deploy; failures only log (the deploy
// itself succeeded).
func (r *Repository) markDeployed(appID, sha string) {
	if sha == "" {
		if head, err := gitLog(r.appDataDir(appID)); err == nil && len(head) > 0 {
			sha = head[0].Hash
		}
	}
	if sha == "" {
		return
	}
	if err := writeDeployedMark(r.appDataDir(appID), sha); err != nil {
		r.log.Warn("failed to record deployed revision", "app_id", appID, "error", err)
	}
}

// saveWithoutDeploy is SaveApp minus the compose invocation: write the store,
// commit, materialize secrets, and fix the symlink. Split out so the full
// persistence path is testable without Docker.
func (r *Repository) saveWithoutDeploy(app command.AppPayload) (string, error) {
	hash, _, err := r.save(app)
	return hash, err
}

// save is saveWithoutDeploy with the warnings of the save: backup settings
// dropped because the compose file no longer has their backup.
func (r *Repository) save(app command.AppPayload) (string, []string, error) {
	dir := r.appDataDir(app.AppID)
	_, statErr := os.Stat(dir)
	created := errors.Is(statErr, fs.ErrNotExist) // this save creates the app's folder
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	if err := gitEnsure(dir); err != nil {
		return "", nil, fmt.Errorf("init app repo: %w", err)
	}

	store, err := r.writeAppStore(dir, app)
	if err != nil {
		return "", nil, fmt.Errorf("write app store: %w", err)
	}

	// Git-sourced app: sync the upstream and pin its head BEFORE committing,
	// so the lock lands inside this save's commit.
	if spec := r.appSourceSpec(dir); spec != nil {
		if _, err := r.ensureSource(context.Background(), dir, *spec, r.sourceTokenPlaintext(dir), ""); err != nil {
			return "", nil, fmt.Errorf("sync source: %w", err)
		}
	}

	// The backups must be valid against the compose file being saved (for
	// a git-sourced app, the one just pulled); nothing is committed if not.
	warnings, err := r.cleanBackups(dir)
	if err != nil {
		r.undoSave(dir, created, savedPaths(app))
		return "", nil, err
	}

	name := r.appName(dir, app.AppID)
	hash, err := gitCommitAll(dir, "save "+name)
	if err != nil {
		return "", nil, fmt.Errorf("commit: %w", err)
	}

	if err := r.materializeSecrets(dir, store); err != nil {
		return "", nil, fmt.Errorf("materialize secrets: %w", err)
	}
	if _, err := ensureAppSymlink(r.cfg.GetAppsDir(), "apps-data", app.AppID, name); err != nil {
		r.log.Warn("failed to update app symlink", "app_id", app.AppID, "error", err)
	}
	return hash, warnings, nil
}

// savedPaths are the committed-state files writeAppStore writes for app
// (secret files are written only after the commit).
func savedPaths(app command.AppPayload) []string {
	out := []string{configRel, secretsRel, envRel, gitignoreRel}
	for _, f := range app.Files {
		if f.Encrypted {
			continue
		}
		if rel, err := safeRel(f.ItemName()); err == nil {
			out = append(out, rel)
		}
	}
	return out
}

// undoSave puts an app's folder back the way its last commit left it after
// a save that was refused before committing: tracked files are restored,
// the files the save wrote (written) that the last commit doesn't have are
// removed, and a git-sourced app's source/ goes back to the restored lock.
// A folder this save created (created) is removed whole, so no half-saved
// app is ever listed; a folder that existed before (even without a commit
// yet) only loses what the save wrote.
func (r *Repository) undoSave(dir string, created bool, written []string) {
	if created {
		if err := os.RemoveAll(dir); err != nil {
			r.log.Warn("undo a refused save", "dir", dir, "error", err)
		}
		return
	}
	head, headErr := gitHead(dir)
	if headErr == nil {
		if err := gitRestore(dir, head); err != nil {
			r.log.Warn("undo a refused save", "dir", dir, "error", err)
		}
	}
	for _, rel := range written {
		if headErr == nil && gitTracked(dir, head, rel) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, rel)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			r.log.Warn("undo a refused save", "file", rel, "error", err)
		}
	}
	if headErr != nil {
		return
	}
	// ensureSource already moved source/ to the new upstream head.
	if spec := r.appSourceSpec(dir); spec != nil {
		if lock, ok := readSourceLock(dir); ok {
			if _, err := r.ensureSource(context.Background(), dir, *spec, r.sourceTokenPlaintext(dir), lock.SHA); err != nil {
				r.log.Warn("undo a refused save: restore source", "dir", dir, "error", err)
			}
		}
	}
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
	r.markDeployed(appID, newHead)
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

// GetAppsStatus reports container status for every app the agent has, using
// ONE `docker ps` invocation for all of them (grouped by the compose project
// label) — this runs on a timer forever, so per-app compose forks are off the
// table. An exec failure fails the whole collection: the reporter skips the
// tick rather than flooding "unknown" while Docker restarts.
func (r *Repository) GetAppsStatus(ctx context.Context) ([]command.AppStatus, error) {
	entries, err := os.ReadDir(r.cfg.GetAppsDataDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []command.AppStatus{}, nil
		}
		return nil, err
	}

	byProject, err := r.dockerPSByProject(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]command.AppStatus, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		appID := e.Name()
		cs := toContainerStatuses(byProject[projectName(appID)])
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
		if n, err := r.commitCount(appID, dir); err == nil {
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

// composeFiles names an app's compose files explicitly, so every invocation
// (and run.sh, and anything appended later) loads exactly them. A git-sourced
// app uses its compose_path inside source/, else the compose file compose
// would find at the repo root. Otherwise, and when the repo has none, it is
// the file compose auto-detects in the app folder plus its override file
// (model.DetectComposeFiles). nil when there is no compose file at all.
func (r *Repository) composeFiles(dir string, spec *sourceSpec) []string {
	if spec != nil {
		if spec.ComposePath != "" {
			if rel, err := safeRel(spec.ComposePath); err == nil {
				return []string{filepath.Join(sourceDirRel, rel)}
			}
		} else if found := model.DetectComposeFiles(fileIn(filepath.Join(dir, sourceDirRel))); found != nil {
			return []string{filepath.Join(sourceDirRel, found[0])}
		}
	}
	return model.DetectComposeFiles(fileIn(dir))
}

// appComposeFiles is composeFiles for the app in dir.
func (r *Repository) appComposeFiles(dir string) []string {
	return r.composeFiles(dir, r.appSourceSpec(dir))
}

// fileIn reports whether a regular file name exists in dir.
func fileIn(dir string) func(name string) bool {
	return func(name string) bool {
		info, err := os.Stat(filepath.Join(dir, name))
		return err == nil && info.Mode().IsRegular()
	}
}

// composeArgs builds the common CLI prefix. Compose and env files are always
// explicit: with -f pointing into source/, compose's implicit project
// directory moves there and would stop auto-loading the app's committed .env.
// Env files load in order — .env, storage.env, .env.secrets — later ones
// winning.
//
// The app's backup profiles are passed as on a deploy: a custom backup's
// service only exists for compose while its profile is active.
func (r *Repository) composeArgs(appID string, verb ...string) []string {
	dir := r.appDataDir(appID)
	files := r.appComposeFiles(dir)
	return r.composeArgsFor(appID, files, r.appProfiles(dir, files), verb...)
}

// composeArgsFor is composeArgs with the app's compose files already
// resolved and the profiles to activate (--profile; none: compose's own
// COMPOSE_PROFILES handling).
func (r *Repository) composeArgsFor(appID string, files, profiles []string, verb ...string) []string {
	args := []string{"compose", "--project-name", projectName(appID)}
	dir := r.appDataDir(appID)
	for _, f := range files {
		args = append(args, "-f", f)
	}
	args = append(args, "--env-file", envRel)
	for _, rel := range []string{storageEnvRel, envSecretsRel} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			args = append(args, "--env-file", rel)
		}
	}
	for _, p := range profiles {
		args = append(args, "--profile", p)
	}
	return append(args, verb...)
}

func (r *Repository) composeRun(ctx context.Context, appID string, verb ...string) error {
	return r.composeExec(ctx, appID, r.composeArgs(appID, verb...))
}

// composeExec runs docker with args in the app's folder.
func (r *Repository) composeExec(ctx context.Context, appID string, args []string) error {
	return r.runCompose(ctx, r.appDataDir(appID), args)
}

// dockerIn runs docker with args in dir; the error carries its output.
func dockerIn(ctx context.Context, dir string, args []string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (r *Repository) composeUp(ctx context.Context, appID string) error {
	dir := r.appDataDir(appID)
	files := r.appComposeFiles(dir)
	// The backups are validated on every deploy: a git-sourced app's compose
	// file changes upstream.
	cfg, err := r.appBackups(dir, files)
	if err != nil {
		return err
	}
	// The storage variables are resolved on every deploy: a storage removed
	// or changed since the last one must not be served stale.
	if err := r.materializeStorageEnv(ctx, dir, files, cfg.backups); err != nil {
		return err
	}
	// Every enabled sqlite and files backup must reach its data through the
	// app's own mounts.
	if _, err := r.resolveBackups(ctx, appID, files, cfg.backups); err != nil {
		return err
	}
	profiles := r.composeProfiles(dir, cfg.backups)
	// Refresh the manual-run helper alongside every deploy; never fail one on it.
	if err := r.writeRunHelper(appID, files, profiles); err != nil {
		r.log.Warn("write manual-run helper", "app_id", appID, "error", err)
	}
	if err := r.composeExec(ctx, appID, r.composeArgsFor(appID, files, profiles, "up", "-d", "--remove-orphans")); err != nil {
		return err
	}
	return r.stopOffServices(ctx, appID, files, cfg, activeProfiles(dir, profiles))
}

// composeDown is a no-op (nil) if the app dir does not exist. volumes=true
// adds -v (see composeDownVerb).
func (r *Repository) composeDown(ctx context.Context, appID string, volumes bool) error {
	if _, err := os.Stat(r.appDataDir(appID)); err != nil {
		return nil
	}
	// Every profile is activated: down removes all of the project's services,
	// whatever the recipe says or whether it can be read at all.
	dir := r.appDataDir(appID)
	args := r.composeArgsFor(appID, r.appComposeFiles(dir), []string{"*"}, composeDownVerb(volumes)...)
	return r.composeExec(ctx, appID, args)
}

func (r *Repository) composeRestart(ctx context.Context, appID string) error {
	return r.composeRun(ctx, appID, "restart")
}

func (r *Repository) composePull(ctx context.Context, appID string) error {
	return r.composeRun(ctx, appID, "pull")
}

// dockerPSByProject lists every compose-managed container on the host in one
// `docker ps` exec and groups them by compose project name.
func (r *Repository) dockerPSByProject(ctx context.Context) (map[string][]composePS, error) {
	cmd := exec.CommandContext(ctx, "docker", "ps", "--all", "--no-trunc",
		"--filter", "label="+composeProjectLabel, "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseDockerPS(out)
}

// projectName derives a stable docker compose project name from an app id.
func projectName(appID string) string {
	return wfProjectPrefix + filepath.Base(appID)
}
