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
// warnings. It holds the app's operation lock for the whole run (save and
// compose up), giving up after the busy wait.
func (r *Repository) SaveAppResult(ctx context.Context, app command.AppPayload, draft bool) (string, []string, error) {
	unlock, err := r.lockForMutation(ctx, app.AppID)
	if err != nil {
		return "", nil, err
	}
	defer unlock()
	end, err := r.beginOp(app.AppID)
	if err != nil {
		return "", nil, err
	}
	defer end()
	hash, warnings, err := r.save(app, !draft)
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
// commit, materialize secrets and shared variables, and fix the symlink.
// Split out so the full persistence path is testable without Docker.
func (r *Repository) saveWithoutDeploy(app command.AppPayload) (string, error) {
	hash, _, err := r.save(app, true)
	return hash, err
}

// save is saveWithoutDeploy with the warnings of the save: backup settings
// dropped because the compose file no longer has their backup. forDeploy
// also renders the shared variables: a draft deploys nothing, so its shared
// files (and stale flag) stay as deployed.
//
// A git-sourced app's upstream is synced first, before the app's lock is
// taken (the clone or fetch can take minutes and must not hold up the other
// writers of the app's files); the files are then written and committed in one
// step under it.
func (r *Repository) save(app command.AppPayload, forDeploy bool) (string, []string, error) {
	// The pins the save carries: without a ports key, the stored ones stay.
	pins, pinsSent, err := model.ParsePorts(app.Config)
	if err != nil {
		return "", nil, err
	}
	dir := r.appDataDir(app.AppID)
	_, statErr := os.Stat(dir)
	created := errors.Is(statErr, fs.ErrNotExist) // this save creates the app's folder
	if spec := payloadSourceSpec(app.Config); spec != nil {
		if err := r.syncSourceForSave(dir, app, *spec); err != nil {
			if created {
				// No half-saved app is ever listed.
				_ = os.RemoveAll(dir)
			}
			return "", nil, fmt.Errorf("sync source: %w", err)
		}
	}

	// One writer of the app's files at a time (a backups update checks its
	// base against them).
	unlock, _ := r.lockApp(context.Background(), app.AppID)
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	if err := gitEnsure(dir); err != nil {
		return "", nil, fmt.Errorf("init app repo: %w", err)
	}

	r.ensurePortIndex() // before writeAppStore replaces config.json
	prevPorts, err := r.readPorts(dir)
	if err != nil {
		return "", nil, err
	}
	if !pinsSent {
		pins = pinnedOnly(prevPorts)
	}
	store, err := r.writeAppStore(dir, app)
	if err != nil {
		return "", nil, fmt.Errorf("write app store: %w", err)
	}

	// The x-winterflow block and the backups must be valid against the
	// compose file being saved (for a git-sourced app, the one just pulled),
	// an ingress hostname may only point at one of its http expose entries,
	// and every expose entry gets its host port; nothing is committed if not.
	spec, warnings, err := r.cleanBackups(dir)
	if err == nil {
		err = r.applyExpose(app.AppID, dir, app.Config, spec, prevPorts, pins)
	}
	if err != nil {
		restoreSource := r.undoSave(dir, created, savedPaths(app))
		unlock()
		locked = false
		if restoreSource {
			r.restoreSource(app.AppID, dir)
		}
		return "", nil, err
	}

	name := r.appName(dir, app.AppID)
	hash, err := gitCommitAll(dir, "save "+name)
	if err != nil {
		return "", nil, fmt.Errorf("commit: %w", err)
	}

	if forDeploy {
		err = r.materializeForDeploy(dir, store)
	} else if err = r.materializeSecrets(dir, store); err != nil {
		err = fmt.Errorf("materialize secrets: %w", err)
	}
	// The override file follows the compose files just committed, a draft's
	// too: until the next deploy, every compose command passes it.
	r.refreshComposeOverride(app.AppID, dir)
	if err != nil {
		return "", nil, err
	}
	if _, err := ensureAppSymlink(r.cfg.GetAppsDir(), "apps-data", app.AppID, name); err != nil {
		r.log.Warn("failed to update app symlink", "app_id", app.AppID, "error", err)
	}
	return hash, warnings, nil
}

// payloadSourceSpec is the source configuration of the config blob a save
// carries; nil for an editor-authored app (or an unreadable blob, which
// writeAppStore and cleanBackups then report).
func payloadSourceSpec(config []byte) *sourceSpec {
	var cfg map[string]any
	if json.Unmarshal(config, &cfg) != nil {
		return nil
	}
	return sourceFromConfig(cfg)
}

// syncSourceForSave syncs the upstream of the app being saved and pins its
// head, so source.lock lands in the save's commit. It runs before the app's
// lock is taken, on the source's own (see lockSource), with the token the
// payload carries (the stored one for the placeholder) as writeAppStore will
// store it.
func (r *Repository) syncSourceForSave(dir string, app command.AppPayload, spec sourceSpec) error {
	token := ""
	if app.Source != nil {
		ct := string(app.Source.Token)
		if ct == command.EncryptedPlaceholder {
			ct = loadSecretStore(dir).SourceToken
		}
		token = r.decryptSourceToken(ct)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	unlock, _ := r.lockSource(context.Background(), app.AppID)
	defer unlock()
	_, err := r.ensureSource(context.Background(), dir, spec, token, "")
	return err
}

// restoreSource puts source/ back at the commit the app's lock (restored by
// an undone save) names, on the source's own lock: ensureSource is bounded,
// but is network work.
func (r *Repository) restoreSource(appID, dir string) {
	spec := r.appSourceSpec(dir)
	if spec == nil {
		return
	}
	lock, ok := readSourceLock(dir)
	if !ok {
		return
	}
	unlock, _ := r.lockSource(context.Background(), appID)
	defer unlock()
	if _, err := r.ensureSource(context.Background(), dir, *spec, r.sourceTokenPlaintext(dir), lock.SHA); err != nil {
		r.log.Warn("undo a refused save: restore source", "dir", dir, "error", err)
	}
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
// removed. A folder this save created (created) is removed whole, so no
// half-saved app is ever listed; a folder that existed before (even without
// a commit yet) only loses what the save wrote. It reports whether a
// git-sourced app's source/ is to go back to the restored lock
// (restoreSource, which is network work: the caller does it once it has
// released the app's lock), since the save moved it to the new upstream head.
func (r *Repository) undoSave(dir string, created bool, written []string) (restoreSource bool) {
	if created {
		if err := os.RemoveAll(dir); err != nil {
			r.log.Warn("undo a refused save", "dir", dir, "error", err)
		}
		return false
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
	return headErr == nil
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
	unlock, err := r.lockForMutation(ctx, appID)
	if err != nil {
		return "", err
	}
	defer unlock()
	end, err := r.beginOp(appID)
	if err != nil {
		return "", err
	}
	defer end()
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
	unlock, _ := r.lockApp(context.Background(), appID)
	defer unlock()
	dir := r.appDataDir(appID)
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("app %s not found", appID)
	}
	r.ensurePortIndex() // before the restored revision's config.json replaces the current
	if err := gitRestore(dir, hash); err != nil {
		return "", fmt.Errorf("restore %s: %w", hash, err)
	}
	// Git-sourced app: the restored lock names the SHA that was deployed at
	// that revision — put source/ back exactly there.
	if spec := r.appSourceSpec(dir); spec != nil {
		if lock, ok := readSourceLock(dir); ok {
			unlockSource, _ := r.lockSource(context.Background(), appID)
			_, err := r.ensureSource(context.Background(), dir, *spec, r.sourceTokenPlaintext(dir), lock.SHA)
			unlockSource()
			if err != nil {
				return "", fmt.Errorf("restore source: %w", err)
			}
		}
	}
	// The restored revision's ports come back where no other app took them
	// meanwhile (a pin another app holds refuses the rollback).
	if err := r.restorePorts(appID, dir); err != nil {
		return "", err
	}
	short := hash
	if len(short) > 8 {
		short = short[:8]
	}
	newHead, err := gitCommitAll(dir, "rollback to "+short)
	if err != nil {
		return "", fmt.Errorf("commit rollback: %w", err)
	}
	err = r.materializeForDeploy(dir, loadSecretStore(dir))
	r.refreshComposeOverride(appID, dir)
	if err != nil {
		return "", err
	}
	// The restored config may carry a different name — keep the symlink true.
	if _, err := ensureAppSymlink(r.cfg.GetAppsDir(), "apps-data", appID, r.appName(dir, appID)); err != nil {
		r.log.Warn("failed to update app symlink", "app_id", appID, "error", err)
	}
	return newHead, nil
}

// StatusReport is the apps.status body: container status for every app the
// agent has, using ONE `docker ps` invocation for all of them (grouped by the
// compose project label) — this runs on a timer forever, so per-app compose
// forks are off the table. An exec failure fails the whole collection: the
// reporter skips the tick rather than flooding "unknown" while Docker
// restarts. Each app's stale names come with the shared-variables revision
// they were computed against (one consistent pair), so the API never reads a
// just-applied revision beside the previous snapshot's stale set.
func (r *Repository) StatusReport(ctx context.Context) (command.GetAppsStatusResponse, error) {
	entries, err := os.ReadDir(r.cfg.GetAppsDataDir())
	if err != nil {
		if os.IsNotExist(err) {
			_, rev := r.staleSet()
			return command.GetAppsStatusResponse{Apps: []command.AppStatus{}, VarsRevision: &rev}, nil
		}
		return command.GetAppsStatusResponse{}, err
	}

	byProject, err := r.dockerPSByProject(ctx)
	if err != nil {
		return command.GetAppsStatusResponse{}, err
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
	apps, rev := r.withStale(out)
	return command.GetAppsStatusResponse{Apps: apps, VarsRevision: &rev}, nil
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
	unlockSource, _ := r.lockSource(context.Background(), appID)
	sha, err := r.ensureSource(context.Background(), dir, *spec, r.sourceTokenPlaintext(dir), "")
	unlockSource()
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
	// The commit stages everything, so it is made under the app's lock, with
	// no writer between its files and its own commit: it would otherwise take
	// in that writer's half-written files, and two go-git writes to one
	// repository can corrupt its index. It waits without a deadline (the
	// holders are bounded): a poll that gave up would not retry, as the lock
	// it just wrote makes the next poll see nothing new.
	unlock, _ := r.lockApp(context.Background(), appID)
	defer unlock()
	// The upstream compose file may add or drop expose entries. A failure
	// here is left for the deploy to report: the source lock is already
	// written, so a poll that gave up would never commit this update.
	r.ensurePortIndex()
	if spec, err := r.appSpec(dir); err != nil {
		r.log.Warn("source update: read x-winterflow", "app_id", appID, "error", err)
	} else if ports, err := r.readPorts(dir); err != nil {
		r.log.Warn("source update: read ports", "app_id", appID, "error", err)
	} else if err := r.syncPorts(appID, dir, spec.Expose, ports, pinnedOnly(ports)); err != nil {
		r.log.Warn("source update: allocate ports", "app_id", appID, "error", err)
	}
	_, err = gitCommitAll(dir, "update source to "+short)
	r.refreshComposeOverride(appID, dir) // source/ moved either way
	if err != nil {
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
		if r.restoreRunning(appID) {
			continue // polled again once the restore is over
		}
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

		func() {
			// Busy (a reload or a user action holds the app): skip this
			// round rather than stall polling for every other app.
			unlock, err := r.lockForMutation(ctx, appID)
			if err != nil {
				r.log.Info("source poll skipped", "app_id", appID, "error", err)
				return
			}
			defer unlock()
			changed, sha, err := r.refreshSourceWithoutDeploy(appID)
			if err != nil {
				r.log.Warn("source poll failed", "app_id", appID, "error", err)
				return
			}
			if !changed {
				return
			}
			r.log.Info("source updated, redeploying", "app_id", appID, "sha", sha)
			if err := r.deploy(ctx, appID); err != nil {
				r.log.Warn("redeploy after source update failed", "app_id", appID, "error", err)
			}
			updated = append(updated, appID)
		}()
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
// Env files load in order — .env.shared, .env.shared.secrets, .env,
// storage.env, .env.secrets — later ones winning: shared values first, so
// the app's own win. The agent-computed storage values sit with the app's
// files, as storages placed them; their WF_STORAGE_/WF_BACKUP_ names are
// reserved on both sides (shared variables may not start with WF_, app
// variables not with WF_STORAGE_/WF_BACKUP_), so no other file can collide
// with them.
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
// COMPOSE_PROFILES handling). The x-winterflow override file, when there is
// one (every commit and deploy re-renders it), is the last -f.
func (r *Repository) composeArgsFor(appID string, files, profiles []string, verb ...string) []string {
	return r.composeArgsWith(appID, files, profiles, true, verb...)
}

// composeArgsWith is composeArgsFor, passing the override file only with
// override.
func (r *Repository) composeArgsWith(appID string, files, profiles []string, override bool, verb ...string) []string {
	args := []string{"compose", "--project-name", projectName(appID)}
	dir := r.appDataDir(appID)
	for _, f := range files {
		args = append(args, "-f", f)
	}
	for _, f := range []string{envSharedRel, envSharedSecretsRel} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			args = append(args, "--env-file", f)
		}
	}
	args = append(args, "--env-file", envRel)
	for _, rel := range []string{storageEnvRel, envSecretsRel} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			args = append(args, "--env-file", rel)
		}
	}
	// The x-winterflow override file goes last, over every compose file.
	if _, err := os.Stat(filepath.Join(dir, composeOverrideRel)); err == nil && override {
		args = append(args, "-f", composeOverrideRel)
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

// dockerWaitDelay is how long a docker command's pipes may stay open after
// its context ended: `docker compose` runs a plugin as a child that keeps
// the pipes of a killed CLI open, which would hold Wait past the deadline.
const dockerWaitDelay = 5 * time.Second

// dockerCommand is docker with args in dir, bound to ctx.
func dockerCommand(ctx context.Context, dir string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	cmd.WaitDelay = dockerWaitDelay
	return cmd
}

// dockerIn runs docker with args in dir; the error carries its output.
func dockerIn(ctx context.Context, dir string, args []string) error {
	cmd := dockerCommand(ctx, dir, args)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (r *Repository) composeUp(ctx context.Context, appID string) error {
	// The last line of defence: a restore's own start goes through
	// composeRun, so this refuses only other callers.
	if err := r.refuseDuringRestore(appID); err != nil {
		return err
	}
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
	// The x-winterflow block is applied through its override file, built
	// from the files just read and the env files just written.
	if err := r.writeComposeOverride(dir, cfg.spec); err != nil {
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
	// whatever the recipe says or whether it can be read at all. The
	// override file is left out: it only adds ports and environment to the
	// services, which down doesn't need, so a stop or a delete never depends
	// on it.
	dir := r.appDataDir(appID)
	args := r.composeArgsWith(appID, r.appComposeFiles(dir), []string{"*"}, false, composeDownVerb(volumes)...)
	return r.composeExec(ctx, appID, args)
}

func (r *Repository) composeRestart(ctx context.Context, appID string) error {
	if err := r.refuseDuringRestore(appID); err != nil {
		return err
	}
	return r.composeRun(ctx, appID, "restart")
}

func (r *Repository) composePull(ctx context.Context, appID string) error {
	return r.composeRun(ctx, appID, "pull")
}

// dockerPSByProject lists every compose-managed container on the host in one
// `docker ps` exec and groups them by compose project name.
func (r *Repository) dockerPSByProject(ctx context.Context) (map[string][]composePS, error) {
	return r.dockerPSLabel(ctx, composeProjectLabel)
}

// dockerPSLabel is dockerPSByProject restricted to containers matching a
// `--filter label=` expression ("key" or "key=value").
func (r *Repository) dockerPSLabel(ctx context.Context, label string) (map[string][]composePS, error) {
	cmd := exec.CommandContext(ctx, "docker", "ps", "--all", "--no-trunc",
		"--filter", "label="+label, "--format", "json")
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
