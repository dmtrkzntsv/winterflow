package dockercompose

import (
	"context"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/backup/recipe"
)

// updateRepo is an app with backupCompose deployed (its mark at HEAD),
// storages "disk" and "hetzner", fake compose config, and fake hooks for
// which projects run and redeploys.
func updateRepo(t *testing.T) (*Repository, *fakeComposeHooks) {
	t.Helper()
	r := newTestRepo(t)
	for _, name := range []string{"disk", "hetzner"} {
		if err := r.storages.Save(localStorage(name, t.TempDir()), nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := saveConfig(t, r, "app-1", backupCompose, plainConfig); err != nil {
		t.Fatal(err)
	}
	r.markDeployed("app-1", "")
	(&fakeComposeConfig{out: configJSON}).install(r)
	hooks := &fakeComposeHooks{up: map[string]bool{"wf-app-1": true}}
	hooks.install(r)
	return r, hooks
}

func update(r *Repository, list ...recipe.BackupConfig) (string, []string, bool, error) {
	resp, err := r.UpdateBackups(context.Background(), "app-1", list)
	return resp.Revision, resp.Warnings, resp.Redeployed, err
}

// Turning on a sqlite backup changes nothing that runs: a commit, no
// redeploy, and the commit counts as deployed.
func TestUpdateBackupsWithoutRedeploy(t *testing.T) {
	r, hooks := updateRepo(t)
	dir := r.appDataDir("app-1")
	resp, err := r.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{
		{Name: "database", Enabled: true, Storage: "disk", Every: "1h"}, // every equals the default: kept, as the user set it
	})
	if err != nil {
		t.Fatal(err)
	}
	head, _ := gitHead(dir)
	if resp.Revision != head || resp.Redeployed || len(hooks.redeploys) != 0 || r.hasDraft("app-1") {
		t.Fatalf("resp = %+v, redeploys %v, draft %v", resp, hooks.redeploys, r.hasDraft("app-1"))
	}
	if len(resp.Backups) != 3 || !resp.Backups[0].Enabled || resp.Backups[0].Storage != "disk" {
		t.Fatalf("backups = %+v", resp.Backups)
	}
	got, _ := r.readOverrides(dir)
	if len(got) != 1 || got[0].Every != "1h" || !got[0].Enabled {
		t.Fatalf("stored = %+v", got)
	}
}

// Turning a custom backup on or off changes the compose profiles: a running
// app is redeployed.
func TestUpdateBackupsRedeploysOnProfileChange(t *testing.T) {
	r, hooks := updateRepo(t)
	resp, err := r.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{{Name: "replica", Enabled: true, Storage: "hetzner"}})
	if err != nil || !resp.Redeployed || len(hooks.redeploys) != 1 {
		t.Fatalf("resp = %+v, %v, redeploys %v", resp, err, hooks.redeploys)
	}
	// A failed redeploy is a warning: the settings are saved.
	hooks.failUp = map[string]error{"app-1": errors.New("port in use")}
	resp, err = r.UpdateBackups(context.Background(), "app-1", nil)
	if err != nil || resp.Redeployed || len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "redeploy failed: port in use") {
		t.Fatalf("resp = %+v, %v", resp, err)
	}
}

func TestUpdateBackupsLeavesStoppedAndDraftAppsAlone(t *testing.T) {
	r, hooks := updateRepo(t)
	hooks.up = map[string]bool{}
	resp, err := r.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{{Name: "replica", Enabled: true, Storage: "hetzner"}})
	if err != nil || resp.Redeployed || len(hooks.redeploys) != 0 || r.hasDraft("app-1") {
		t.Fatalf("stopped: %+v, %v", resp, err)
	}
	// A draft ahead of what runs: never deployed behind the user's back.
	hooks.up = map[string]bool{"wf-app-1": true}
	writeFileT(t, r.appDataDir("app-1"), "notes.txt", "draft")
	if _, err := gitCommitAll(r.appDataDir("app-1"), "draft"); err != nil {
		t.Fatal(err)
	}
	resp, err = r.UpdateBackups(context.Background(), "app-1", nil)
	if err != nil || resp.Redeployed || len(hooks.redeploys) != 0 || len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "undeployed changes") {
		t.Fatalf("draft: %+v, %v", resp, err)
	}
	// docker ps failing: a warning, no redeploy.
	r2, hooks2 := updateRepo(t)
	hooks2.psErr = errors.New("daemon down")
	resp, err = r2.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{{Name: "replica", Enabled: true, Storage: "hetzner"}})
	if err != nil || resp.Redeployed || len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "daemon down") {
		t.Fatalf("ps error: %+v, %v", resp, err)
	}
}

// Nothing is written when the list can't be used.
func TestUpdateBackupsRefuses(t *testing.T) {
	r, _ := updateRepo(t)
	head, _ := gitHead(r.appDataDir("app-1"))
	for _, tc := range []struct {
		list []recipe.BackupConfig
		want string
	}{
		{[]recipe.BackupConfig{{Name: "gone", Every: "1h"}}, `the compose file has no backup "gone": a new backup needs its type`},
		{[]recipe.BackupConfig{{Name: "database", Enabled: true, Storage: "nas"}}, `backup database: there is no storage "nas" on this server`},
		{[]recipe.BackupConfig{{Name: "media", Enabled: true, Storage: "disk", Paths: []string{"/etc/app"}}}, "/etc/app is not on a mount of service web"},
		{[]recipe.BackupConfig{{Name: "database", Every: "1m"}}, "backups[0].every: 1m is too often"},
	} {
		if _, _, _, err := update(r, tc.list...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.list, err, tc.want)
		}
	}
	if now, _ := gitHead(r.appDataDir("app-1")); now != head {
		t.Fatal("a refused update committed")
	}
	if _, err := r.UpdateBackups(context.Background(), "gone", nil); err == nil {
		t.Fatal("a missing app was updated")
	}
}

func TestBackupPlan(t *testing.T) {
	r := newTestRepo(t)
	if plan, err := r.BackupPlan(); err != nil || len(plan) != 0 {
		t.Fatalf("no apps: %v %v", plan, err)
	}
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"database","enabled":true,"storage":"disk"}]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := saveConfig(t, r, "app-2", "services: {}\n", plainConfig); err != nil {
		t.Fatal(err)
	}
	if _, err := saveConfig(t, r, "app-3", backupCompose, plainConfig); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, r.appDataDir("app-3"), "compose.yml", strings.Replace(backupCompose, "volume: data", "volume: gone", 1))
	plan, err := r.BackupPlan()
	if err == nil || !strings.Contains(err.Error(), "app-3: ") {
		t.Fatalf("err = %v", err)
	}
	if len(plan) != 3 || plan[0].AppID != "app-1" || plan[0].Name != "database" || !plan[0].Enabled || plan[2].Name != "replica" {
		t.Fatalf("plan = %+v", plan)
	}
}
