package dockercompose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
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
	resp, err := r.UpdateBackups(context.Background(), "app-1", list, "")
	return resp.Revision, resp.Warnings, resp.Redeployed, err
}

// Turning on a sqlite backup changes nothing that runs: a commit, no
// redeploy, and the commit counts as deployed.
func TestUpdateBackupsWithoutRedeploy(t *testing.T) {
	r, hooks := updateRepo(t)
	dir := r.appDataDir("app-1")
	resp, err := r.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{
		{Name: "database", Enabled: true, Storage: "disk", Every: "1h"}, // every equals the default: kept, as the user set it
	}, "")
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
	resp, err := r.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{{Name: "replica", Enabled: true, Storage: "hetzner"}}, "")
	if err != nil || !resp.Redeployed || len(hooks.redeploys) != 1 {
		t.Fatalf("resp = %+v, %v, redeploys %v", resp, err, hooks.redeploys)
	}
	// A failed redeploy is a warning: the settings are saved.
	hooks.failUp = map[string]error{"app-1": errors.New("port in use")}
	resp, err = r.UpdateBackups(context.Background(), "app-1", nil, "")
	if err != nil || resp.Redeployed || len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "redeploy failed: port in use") {
		t.Fatalf("resp = %+v, %v", resp, err)
	}
}

func TestUpdateBackupsLeavesStoppedAndDraftAppsAlone(t *testing.T) {
	r, hooks := updateRepo(t)
	hooks.up = map[string]bool{}
	resp, err := r.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{{Name: "replica", Enabled: true, Storage: "hetzner"}}, "")
	if err != nil || resp.Redeployed || len(hooks.redeploys) != 0 || r.hasDraft("app-1") {
		t.Fatalf("stopped: %+v, %v", resp, err)
	}
	// A draft ahead of what runs: never deployed behind the user's back.
	hooks.up = map[string]bool{"wf-app-1": true}
	writeFileT(t, r.appDataDir("app-1"), "notes.txt", "draft")
	if _, err := gitCommitAll(r.appDataDir("app-1"), "draft"); err != nil {
		t.Fatal(err)
	}
	resp, err = r.UpdateBackups(context.Background(), "app-1", nil, "")
	if err != nil || resp.Redeployed || len(hooks.redeploys) != 0 || len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "undeployed changes") {
		t.Fatalf("draft: %+v, %v", resp, err)
	}
	// docker ps failing: a warning, no redeploy.
	r2, hooks2 := updateRepo(t)
	hooks2.psErr = errors.New("daemon down")
	resp, err = r2.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{{Name: "replica", Enabled: true, Storage: "hetzner"}}, "")
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
		{[]recipe.BackupConfig{{Name: "media", Enabled: true, Storage: "disk", Paths: ptrList("/etc/app")}}, "/etc/app is not on a mount of service web"},
		{[]recipe.BackupConfig{{Name: "database", Every: "1m"}}, "backups[0].every: 1m is too often"},
	} {
		if _, _, _, err := update(r, tc.list...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.list, err, tc.want)
		}
	}
	if now, _ := gitHead(r.appDataDir("app-1")); now != head {
		t.Fatal("a refused update committed")
	}
	if _, err := r.UpdateBackups(context.Background(), "gone", nil, ""); err == nil {
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

// A commit that fails leaves the previous config.json in the working tree, so
// the next deploy does not apply settings the caller was told failed.
func TestUpdateBackupsRestoresTheConfigWhenTheCommitFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not stop root")
	}
	r, _ := updateRepo(t)
	dir := r.appDataDir("app-1")
	cfgPath := filepath.Join(dir, configRel)
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// Git can't write objects: every directory under .git/objects is read-only.
	objects := filepath.Join(dir, ".git", "objects")
	lock := func(mode os.FileMode) {
		_ = filepath.WalkDir(objects, func(p string, d fs.DirEntry, _ error) error {
			if d != nil && d.IsDir() {
				_ = os.Chmod(p, mode)
			}
			return nil
		})
	}
	lock(0o500)
	t.Cleanup(func() { lock(0o700) })
	_, err = r.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{{Name: "database", Enabled: true, Storage: "disk"}}, "")
	if err == nil || !strings.Contains(err.Error(), "commit:") {
		t.Fatalf("err = %v", err)
	}
	after, _ := os.ReadFile(cfgPath)
	if string(after) != string(before) {
		t.Fatalf("config.json = %s, want %s", after, before)
	}
}

// A config.json of `null` (older saves could write one) must not crash
// backup.update: the backups list is written into a fresh object.
func TestUpdateBackupsOnNullConfig(t *testing.T) {
	r, _ := updateRepo(t)
	dir := r.appDataDir("app-1")
	if err := os.WriteFile(filepath.Join(dir, configRel), []byte("null"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{{Name: "database", Every: "2h"}}, ""); err != nil {
		t.Fatal(err)
	}
	got, err := r.readOverrides(dir)
	if err != nil || len(got) != 1 || got[0].Every != "2h" {
		t.Fatalf("stored = %+v, %v", got, err)
	}
}

func ptrList(s ...string) *[]string { return &s }

const volumeFilesCompose = `services:
  web:
    image: example/web
    volumes: [data:/data]
volumes:
  data:
x-winterflow:
  backups:
    - name: media
      type: files
      volume: data
      paths: [uploads]
      exclude: ["*.tmp"]
`

// What backup.update answers is what is stored: an override that widens a
// files backup to the whole volume (paths: []) or clears the recipe's
// excludes (exclude: []) must survive config.json, not fall back to the
// recipe's lists on the next read.
func TestUpdateBackupsExplicitEmptyListsRoundTrip(t *testing.T) {
	r, _ := updateRepo(t)
	if _, err := saveConfig(t, r, "app-1", volumeFilesCompose, plainConfig); err != nil {
		t.Fatal(err)
	}
	r.markDeployed("app-1", "")
	dir := r.appDataDir("app-1")
	for name, o := range map[string]recipe.BackupConfig{
		"both empty":    {Name: "media", Paths: ptrList(), Exclude: ptrList()},
		"paths only":    {Name: "media", Paths: ptrList()},
		"exclude only":  {Name: "media", Exclude: ptrList()},
		"inherit both":  {Name: "media"},
		"replace lists": {Name: "media", Paths: ptrList("a", "b"), Exclude: ptrList("*.log")},
	} {
		resp, err := r.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{o}, "")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := r.appBackups(dir, r.appComposeFiles(dir))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		stored, _ := json.Marshal(got.backups)
		answered, _ := json.Marshal(resp.Backups)
		if string(stored) != string(answered) {
			t.Errorf("%s: stored %s, answered %s", name, stored, answered)
		}
	}
	// The widened backup really covers the whole volume now.
	resp, _ := r.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{{Name: "media", Paths: ptrList(), Exclude: ptrList()}}, "")
	got, _ := r.appBackups(dir, r.appComposeFiles(dir))
	if len(got.backups[0].Paths) != 0 || len(got.backups[0].Exclude) != 0 || got.backups[0].FromRecipe["paths"] || got.backups[0].FromRecipe["exclude"] {
		t.Fatalf("stored = %+v (answered %+v)", got.backups[0], resp.Backups[0])
	}
}

// An update carries the base it was made from: a list changed since is
// refused, the current base is accepted, and none skips the check.
func TestUpdateBackupsBase(t *testing.T) {
	r, _ := updateRepo(t)
	dir := r.appDataDir("app-1")
	ctx := context.Background()
	on := []recipe.BackupConfig{{Name: "database", Enabled: true, Storage: "disk"}}

	first, err := r.UpdateBackups(ctx, "app-1", on, "")
	if err != nil || first.BackupsBase == "" {
		t.Fatalf("empty base: %+v, %v", first, err)
	}
	stored, _ := r.readOverrides(dir)
	if first.BackupsBase != backupsBase(stored) {
		t.Fatalf("response base %q != stored %q", first.BackupsBase, backupsBase(stored))
	}
	if got, err := r.GetApp(ctx, "app-1"); err != nil || got.BackupsBase != first.BackupsBase {
		t.Fatalf("app.get base = %q, %v; want %q", got.BackupsBase, err, first.BackupsBase)
	}

	// The same base is accepted, and moves on.
	off := []recipe.BackupConfig{}
	second, err := r.UpdateBackups(ctx, "app-1", off, first.BackupsBase)
	if err != nil || second.BackupsBase == first.BackupsBase || second.BackupsBase != backupsBase(nil) {
		t.Fatalf("matching base: %+v, %v", second, err)
	}

	// The first base is stale now: refused, and nothing is written.
	head, _ := gitHead(dir)
	if _, err := r.UpdateBackups(ctx, "app-1", on, first.BackupsBase); !errors.Is(err, command.ErrBackupsChanged) {
		t.Fatalf("stale base: %v", err)
	}
	if after, _ := gitHead(dir); after != head {
		t.Fatalf("a refused update committed: %s -> %s", head, after)
	}
}

// Two saves made from the same base cannot both succeed, however they
// interleave on the distributed agent (one goroutine per request): exactly
// one wins and the other is refused as stale, instead of the later write
// silently replacing the earlier.
func TestUpdateBackupsSameBaseOneWins(t *testing.T) {
	r, _ := updateRepo(t)
	ctx := context.Background()
	base := backupsBase(nil)
	for round := range 30 {
		errs := make([]error, 2)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				list := []recipe.BackupConfig{{Name: "database", Enabled: true, Storage: "disk", Every: fmt.Sprintf("%dm", 10+round*2+i)}}
				_, errs[i] = r.UpdateBackups(ctx, "app-1", list, base)
			}()
		}
		close(start)
		wg.Wait()
		wins, stale := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				wins++
			case errors.Is(err, command.ErrBackupsChanged):
				stale++
			default:
				t.Fatalf("round %d: %v", round, err)
			}
		}
		if wins != 1 || stale != 1 {
			t.Fatalf("round %d: %d won, %d refused as stale (%v)", round, wins, stale, errs)
		}
		stored, _ := r.readOverrides(r.appDataDir("app-1"))
		base = backupsBase(stored)
	}
}

// The writers of an app's files wait for each other (and for ctx).
func TestLockAppSerializesAndHonorsContext(t *testing.T) {
	r := newTestRepo(t)
	unlock, err := r.lockApp(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := r.lockApp(ctx, "app-1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock: %v", err)
	}
	if other, err := r.lockApp(context.Background(), "app-2"); err != nil {
		t.Fatalf("another app: %v", err)
	} else {
		other()
	}
	unlock()
	again, err := r.lockApp(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	again()
	r.forgetApp("app-1")
	r.lockMu.Lock()
	left := len(r.appLocks)
	r.lockMu.Unlock()
	if left != 0 { // a lock exists only while it is held or awaited
		t.Fatalf("locks left: %d", left)
	}
}

// forgetApp (a delete) must not hand a second holder the lock of an app id
// that is created again while the first still holds it: the lock stays one
// entry, and goes only when nobody holds or awaits it.
func TestForgetAppKeepsAHeldLock(t *testing.T) {
	r := newTestRepo(t)
	unlock, err := r.lockApp(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	r.forgetApp("app-1")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := r.lockApp(ctx, "app-1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a second holder got the lock of the app while it was held: %v", err)
	}
	// A waiter that gave up leaves no entry behind; the holder's release does.
	unlock()
	r.lockMu.Lock()
	left := len(r.appLocks)
	r.lockMu.Unlock()
	if left != 0 {
		t.Fatalf("locks left: %d", left)
	}
	// The source's lock is a separate one.
	a, _ := r.lockApp(context.Background(), "app-1")
	defer a()
	src, err := r.lockSource(context.Background(), "app-1")
	if err != nil {
		t.Fatalf("source lock beside the app's: %v", err)
	}
	src()
}

// The base in an update's answer is the written list's even when the stored
// file cannot be read back.
func TestBaseAfterWriteFallsBackToWhatWasWritten(t *testing.T) {
	r, _ := updateRepo(t)
	dir := r.appDataDir("app-1")
	written := []recipe.BackupConfig{{Name: "database", Enabled: true, Storage: "disk"}}
	if err := os.WriteFile(filepath.Join(dir, configRel), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := r.baseAfterWrite(dir, written); got == "" || got != backupsBase(written) {
		t.Fatalf("base = %q, want %q", got, backupsBase(written))
	}
}
