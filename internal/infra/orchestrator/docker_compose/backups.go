package dockercompose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/layout"
	"winterflow/internal/infra/backup/store"
	"winterflow/pkg/fsutil"
)

// backupUpdateBudget bounds backup.update, its redeploy included.
const backupUpdateBudget = 10 * time.Minute

// withDeadlineValue runs fn in a goroutine that is abandoned when the
// deadline d comes first (see detached): storage calls on a hung network
// mount ignore their context, and these run on the command pipeline. fn's
// value travels over a channel, so an abandoned fn shares nothing with the
// caller.
func withDeadlineValue[T any](parent context.Context, d time.Duration, what string, fn func(ctx context.Context) (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ctx, cancel := context.WithTimeout(parent, d)
	defer cancel()
	done := make(chan result, 1)
	go func() {
		v, err := fn(ctx)
		done <- result{v, err}
	}()
	select {
	case res := <-done:
		return res.v, res.err
	case <-ctx.Done():
		var zero T
		if err := parent.Err(); err != nil {
			return zero, fmt.Errorf("%s: %w", what, err)
		}
		return zero, fmt.Errorf("%s: the storage did not answer within %s", what, d)
	}
}

// withDeadline is withDeadlineValue for a call with no value.
func withDeadline(parent context.Context, d time.Duration, what string, fn func(ctx context.Context) error) error {
	_, err := withDeadlineValue(parent, d, what, func(ctx context.Context) (struct{}, error) { return struct{}{}, fn(ctx) })
	return err
}

// backupStorage finds an app's backup (any state) and opens the storage it
// names: what listing and deleting restore points need.
func (r *Repository) backupStorage(appID, name string) (store.Store, string, error) {
	if !r.appExists(appID) {
		return nil, "", fmt.Errorf("app %s not found", appID)
	}
	dir := r.appDataDir(appID)
	cfg, err := r.appBackups(dir, r.appComposeFiles(dir))
	if err != nil {
		return nil, "", err
	}
	b, ok := recipe.Declared(cfg.backups)[name]
	switch {
	case !ok:
		return nil, "", fmt.Errorf("the app has no backup %s", name)
	case !b.Builtin():
		return nil, "", fmt.Errorf("the backup %s is a custom backup: its copies are kept by its own tool", name)
	case b.Storage == "":
		return nil, "", fmt.Errorf("the backup %s has no storage", name)
	}
	st, err := r.openBackupStorage(b.Storage)
	return st, layout.Folder(appID, name), err
}

// ListRestorePoints lists a backup's complete restore points, newest first
// (30 seconds; on request only).
func (r *Repository) ListRestorePoints(ctx context.Context, appID, name string) ([]command.RestorePoint, error) {
	return withDeadlineValue(ctx, storageCallTimeout, "list the restore points", func(ctx context.Context) ([]command.RestorePoint, error) {
		st, folder, err := r.backupStorage(appID, name)
		if err != nil {
			return nil, err
		}
		points, err := listFolder(ctx, st, folder)
		if err != nil {
			return nil, err
		}
		out := []command.RestorePoint{}
		for _, p := range layout.Complete(points) {
			out = append(out, p.Info(r.cfg.GetBackupRetentionDays()))
		}
		return out, nil
	})
}

// DeleteRestorePoint deletes one restore point (any kind, complete or not)
// and its manifest (30 seconds).
func (r *Repository) DeleteRestorePoint(ctx context.Context, appID, name, id string) error {
	return withDeadline(ctx, storageCallTimeout, "delete the restore point", func(ctx context.Context) error {
		st, folder, err := r.backupStorage(appID, name)
		if err != nil {
			return err
		}
		points, err := listFolder(ctx, st, folder)
		if err != nil {
			return err
		}
		for _, p := range points {
			if p.ID == id {
				return deletePoints(ctx, st, []layout.Point{p})
			}
		}
		return fmt.Errorf("the backup %s has no restore point %s", name, id)
	})
}

// UpdateBackups replaces an app's backups list (its overrides) and commits
// it. The list is checked against the compose file's recipe, the server's
// storages and the app's mounts first; nothing is written when it fails. A
// running app is brought up again only when its compose profiles or storage
// variables change (a custom backup turned on or off, or one its compose
// file references through WF_BACKUP_ moved), never when it has undeployed
// changes (a warning). Warnings also name the overrides dropped because the
// recipe no longer has their backup.
func (r *Repository) UpdateBackups(ctx context.Context, appID string, overrides []recipe.BackupConfig) (command.UpdateBackupsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, backupUpdateBudget)
	defer cancel()
	resp := command.UpdateBackupsResponse{AppID: appID}
	if err := r.refuseDuringRestore(appID); err != nil {
		return resp, err
	}
	if !r.appExists(appID) {
		return resp, fmt.Errorf("app %s not found", appID)
	}
	dir := r.appDataDir(appID)
	files := r.appComposeFiles(dir)
	contents, err := composeContents(dir, files)
	if err != nil {
		return resp, fmt.Errorf("read compose files: %w", err)
	}
	comp, err := recipe.ParseCompose(contents...)
	if err != nil {
		return resp, err
	}
	backups, err := recipe.Check(comp, overrides)
	if err != nil {
		return resp, err
	}
	for _, b := range backups {
		if !b.Enabled {
			continue
		}
		if _, ok, err := r.storages.Get(b.Storage); err != nil {
			return resp, err
		} else if !ok {
			return resp, fmt.Errorf("backup %s: there is no storage %q on this server", b.Name, b.Storage)
		}
	}
	if _, err := r.resolveBackups(ctx, appID, files, backups); err != nil {
		return resp, err
	}
	prev, _ := r.appBackups(dir, files)
	oldEnv, _ := r.renderStorageEnv(ctx, dir, files, prev.backups, false)
	newEnv, err := r.renderStorageEnv(ctx, dir, files, backups, false)
	if err != nil {
		return resp, err
	}
	changed := !bytes.Equal(oldEnv, newEnv) || !slices.Equal(r.composeProfiles(dir, prev.backups), r.composeProfiles(dir, backups))

	mark, marked := readDeployedMark(dir)
	prevHead, _ := gitHead(dir)
	deployed := marked && mark == prevHead
	kept, warnings := recipe.Clean(comp, overrides)
	prevConfig, readErr := os.ReadFile(filepath.Join(dir, configRel))
	if err := r.writeOverrides(dir, kept); err != nil {
		return resp, err
	}
	hash, err := gitCommitAll(dir, "update backups")
	if err != nil {
		// Leave the working tree as it was: compose and the next deploy read
		// it, and must not apply settings the caller was told failed.
		if readErr == nil {
			_ = fsutil.WriteFile(filepath.Join(dir, configRel), prevConfig, 0o644)
		}
		return resp, fmt.Errorf("commit: %w", err)
	}
	resp.Revision, resp.Backups, resp.Warnings = hash, backups, warnings
	if !deployed {
		if changed && marked {
			resp.Warnings = append(resp.Warnings, "the app has undeployed changes and was not redeployed: deploy it to apply the backup settings")
		}
		return resp, nil
	}
	if !changed {
		// What runs is unchanged: the new commit is as deployed as the last.
		r.markDeployed(appID, hash)
		return resp, nil
	}
	psCtx, cancel := context.WithTimeout(ctx, dockerCallTimeout)
	running, err := r.runningProjects(psCtx)
	cancel()
	switch {
	case err != nil:
		resp.Warnings = append(resp.Warnings, "could not check whether the app is running, so it was not redeployed: "+err.Error())
	case !running[projectName(appID)]:
		r.markDeployed(appID, hash) // stopped: its next start deploys this commit
	default:
		if err := r.redeploy(ctx, appID); err != nil {
			resp.Warnings = append(resp.Warnings, "redeploy failed: "+err.Error())
		} else {
			resp.Redeployed = true
			r.markDeployed(appID, hash)
		}
	}
	return resp, nil
}

// PlannedBackup is one backup of one app, as the scheduler plans it.
type PlannedBackup struct {
	AppID string
	recipe.EffectiveBackup
}

// BackupPlan reads every app's effective backups (file reads only: the
// scheduler calls it when something changed, never on a timer). An app
// whose backups can't be read is left out and named in the error.
func (r *Repository) BackupPlan() ([]PlannedBackup, error) {
	entries, err := os.ReadDir(r.cfg.GetAppsDataDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var (
		out      []PlannedBackup
		problems []string
	)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(r.cfg.GetAppsDataDir(), e.Name())
		cfg, err := r.appBackups(dir, r.appComposeFiles(dir))
		if err != nil {
			problems = append(problems, e.Name()+": "+err.Error())
			continue
		}
		for _, b := range cfg.backups {
			out = append(out, PlannedBackup{AppID: e.Name(), EffectiveBackup: b})
		}
	}
	if len(problems) > 0 {
		return out, errors.New(strings.Join(problems, "; "))
	}
	return out, nil
}

// ServiceContainerPrefix starts the names of the containers compose runs for
// an app's service (<project>-<service>-<n>).
func ServiceContainerPrefix(appID, service string) string {
	return projectName(appID) + "-" + service + "-"
}
