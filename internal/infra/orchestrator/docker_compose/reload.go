package dockercompose

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"winterflow/internal/domain/command"
)

// ErrDraftPending refuses a reload while the app has committed but
// undeployed changes: compose up would deploy the draft with the new values.
var ErrDraftPending = errors.New("the app has undeployed changes (a draft); deploy or roll them back, then reload")

// runningApps reports which apps have a running container, from one
// `docker ps` (reload path only; never polled).
func (r *Repository) runningApps(ctx context.Context) (map[string]bool, error) {
	byProject, err := r.dockerPSByProject(ctx)
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	out := map[string]bool{}
	for project, containers := range byProject {
		if !strings.HasPrefix(project, wfProjectPrefix) {
			continue
		}
		for _, c := range containers {
			if c.State == "running" {
				out[strings.TrimPrefix(project, wfProjectPrefix)] = true
				break
			}
		}
	}
	return out, nil
}

// pickCandidates selects, in app-id order, the running apps a reload
// covers: the existing ones among req.AppIDs, else the stale ones (with
// req.Names, only those whose stale names include one of them). Stopped
// apps are left for their next start, which renders anyway.
func pickCandidates(req command.ReloadVarsRequest, running map[string]bool, stale map[string][]string, exists func(string) bool) []string {
	var out []string
	if len(req.AppIDs) > 0 {
		seen := map[string]bool{}
		for _, id := range req.AppIDs {
			if running[id] && !seen[id] && exists(id) {
				seen[id] = true
				out = append(out, id)
			}
		}
		sort.Strings(out)
		return out
	}
	want := map[string]bool{}
	for _, n := range req.Names {
		want[n] = true
	}
	for appID, names := range stale {
		if !running[appID] {
			continue
		}
		if len(want) > 0 && !anyWanted(names, want) {
			continue
		}
		out = append(out, appID)
	}
	sort.Strings(out)
	return out
}

func anyWanted(names []string, want map[string]bool) bool {
	for _, n := range names {
		if want[n] {
			return true
		}
	}
	return false
}

// ReloadCandidates lists the apps a vars.reload covers right now.
func (r *Repository) ReloadCandidates(ctx context.Context, req command.ReloadVarsRequest) ([]string, error) {
	running, err := r.runningApps(ctx)
	if err != nil {
		return nil, err
	}
	return pickCandidates(req, running, r.StaleApps(), r.appExists), nil
}

// appRunning reports whether appID's compose project has a running
// container, from one `docker ps` scoped to that project (reload path
// only; never polled).
func (r *Repository) appRunning(ctx context.Context, appID string) (bool, error) {
	project := projectName(appID)
	byProject, err := r.dockerPSLabel(ctx, composeProjectLabel+"="+project)
	if err != nil {
		return false, fmt.Errorf("docker ps: %w", err)
	}
	for _, c := range byProject[project] {
		if c.State == "running" {
			return true, nil
		}
	}
	return false, nil
}

// ReloadSharedVars re-renders appID's shared-variable files and runs
// `compose up -d`, which recreates only the services whose resolved
// config changed. It returns the names that were stale before the reload.
// An app that is no longer running once the lock is held (stopped since
// the candidates were picked) is skipped: skipped is true and nothing is
// rendered or started — its next start renders anyway. A failed compose up
// leaves the previous containers running, as for any failed deploy.
// Unlike the mutators it waits for the app's lock as long as ctx allows
// (the reload worker bounds ctx per app).
func (r *Repository) ReloadSharedVars(ctx context.Context, appID string) (names []string, skipped bool, err error) {
	unlock, err := r.acquireApp(ctx, appID, 0)
	if err != nil {
		return nil, false, err
	}
	defer unlock()
	if !r.appExists(appID) {
		return nil, false, fmt.Errorf("app %s not found", appID)
	}
	names = r.StaleApps()[appID]
	// A stopped app (a restore stops it too) is skipped before the restore
	// guard: nothing is started on that path.
	running, err := r.appRunning(ctx, appID)
	if err != nil {
		return names, false, err
	}
	if !running {
		r.log.Info("shared-variable reload skipped: app not running", "app_id", appID)
		return names, true, nil
	}
	// A reload starts containers: it is refused while a restore of the app
	// runs, and a restore does not begin while it does.
	end, err := r.beginOp(appID)
	if err != nil {
		return names, false, err
	}
	defer end()
	dir := r.appDataDir(appID)
	if deployed, ok := readDeployedMark(dir); ok {
		head, err := gitHead(dir)
		if err != nil {
			return names, false, fmt.Errorf("read HEAD: %w", err)
		}
		if head != deployed {
			return names, false, ErrDraftPending
		}
	}
	if err := r.renderShared(appID); err != nil {
		return names, false, fmt.Errorf("render shared variables: %w", err)
	}
	if err := r.composeUp(ctx, appID); err != nil {
		return names, false, fmt.Errorf("docker compose up: %w", err)
	}
	return names, false, nil
}
