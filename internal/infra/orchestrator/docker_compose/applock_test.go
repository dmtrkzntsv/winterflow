package dockercompose

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"winterflow/internal/domain/command"
)

func TestLockAppSerializesOneAppOnly(t *testing.T) {
	r := newTestRepo(t)
	var inside, maxInside int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer r.lockAppOp("a")()
			n := atomic.AddInt32(&inside, 1)
			for {
				m := atomic.LoadInt32(&maxInside)
				if n <= m || atomic.CompareAndSwapInt32(&maxInside, m, n) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt32(&inside, -1)
		}()
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("%d holders of one app's lock at once", maxInside)
	}
	// Another app's lock is independent.
	unlockA := r.lockAppOp("a")
	done := make(chan struct{})
	go func() { r.lockAppOp("b")(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("app b waited for app a")
	}
	unlockA()
	r.forgetApp("a")
	r.lockMu.Lock()
	_, kept := r.opLocks["a"]
	r.lockMu.Unlock()
	if kept {
		t.Fatal("forgetApp kept the lock entry")
	}
}

// stubDocker puts a `docker` that exits with code first on PATH.
func stubDocker(t *testing.T, code int) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

// stubDockerApp puts a `docker` first on PATH whose `ps` lists one
// container of appID's project in state, and whose other calls (compose)
// exit with composeCode.
func stubDockerApp(t *testing.T, appID, state string, composeCode int) {
	t.Helper()
	bin := t.TempDir()
	line := `{"ID":"c1","Names":"web","State":"` + state + `","Status":"","Labels":"` +
		composeProjectLabel + `=` + projectName(appID) + `"}`
	script := "#!/bin/sh\nif [ \"$1\" = ps ]; then echo '" + line + "'; exit 0; fi\nexit " + strconv.Itoa(composeCode) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

// TestReleaseIsIdempotent: a doubled release must not free a lock another
// holder has since taken.
func TestReleaseIsIdempotent(t *testing.T) {
	r := newTestRepo(t)
	release := r.lockAppOp("app-1")
	release()
	held := r.lockAppOp("app-1")
	release()
	if _, err := r.acquireApp(context.Background(), "app-1", 10*time.Millisecond); !errors.Is(err, ErrAppBusy) {
		t.Fatalf("a stale release freed the current holder's lock: %v", err)
	}
	held()
}

func draftPayload(appID string) command.AppPayload {
	return command.AppPayload{
		AppID:  appID,
		Config: []byte(`{"name":"Lock App","files":[{"filename":"compose.yml","is_encrypted":false}],"variables":[]}`),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte("services: {}\n")}},
	}
}

// mutators calls every public app mutator once for appID.
func mutators(r *Repository, appID string) map[string]func(context.Context) error {
	return map[string]func(context.Context) error{
		"SaveApp":      func(ctx context.Context) error { _, err := r.SaveApp(ctx, draftPayload(appID)); return err },
		"SaveAppDraft": func(ctx context.Context) error { _, err := r.SaveAppDraft(ctx, draftPayload(appID)); return err },
		"Rollback":     func(ctx context.Context) error { _, err := r.Rollback(ctx, appID, "deadbeef"); return err },
		"StartApp":     func(ctx context.Context) error { return r.StartApp(ctx, appID) },
		"StopApp":      func(ctx context.Context) error { return r.StopApp(ctx, appID) },
		"RestartApp":   func(ctx context.Context) error { return r.RestartApp(ctx, appID) },
		"UpdateApp":    func(ctx context.Context) error { return r.UpdateApp(ctx, appID) },
		"RenameApp":    func(ctx context.Context) error { return r.RenameApp(ctx, appID, "New Name") },
		"DeleteApp":    func(ctx context.Context) error { _, err := r.DeleteApp(ctx, appID, false, false); return err },
		"UpdateBackups": func(ctx context.Context) error {
			_, err := r.UpdateBackups(ctx, appID, nil, "")
			return err
		},
	}
}

func TestMutatorsGiveUpWhenAppBusy(t *testing.T) {
	r := newTestRepo(t)
	r.busyWait = 20 * time.Millisecond
	unlock := r.lockAppOp("app-1")
	defer unlock()
	for name, call := range mutators(r, "app-1") {
		start := time.Now()
		err := call(context.Background())
		if !errors.Is(err, ErrAppBusy) {
			t.Errorf("%s: err = %v, want ErrAppBusy", name, err)
			continue
		}
		if !strings.Contains(err.Error(), "app app-1 is busy with another operation, try again") {
			t.Errorf("%s: message %q", name, err)
		}
		if waited := time.Since(start); waited > time.Second {
			t.Errorf("%s waited %v", name, waited)
		}
	}
	// A cancelled context stops the wait before the deadline.
	r.busyWait = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.StartApp(ctx, "app-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("StartApp with a cancelled ctx: %v", err)
	}
	// Another app is not affected.
	if err := r.StopApp(context.Background(), "app-2"); err != nil {
		t.Fatalf("app-2 blocked by app-1: %v", err)
	}
}

// lockFree asserts appID's lock can be taken right away.
func lockFree(t *testing.T, r *Repository, appID, after string) {
	t.Helper()
	release, err := r.acquireApp(context.Background(), appID, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("lock still held after %s: %v", after, err)
	}
	release()
}

func TestMutatorsReleaseTheLockOnEveryPath(t *testing.T) {
	r := newTestRepo(t)
	// Error paths: compose fails, the app is missing or the revision unknown.
	stubDocker(t, 1)
	for name, call := range mutators(r, "missing") {
		_ = call(context.Background())
		lockFree(t, r, "missing", name+" (missing app)")
		if name != "SaveApp" && name != "SaveAppDraft" {
			continue
		}
		// Undo the folder the save created so the next call sees a missing app.
		_ = os.RemoveAll(r.appDataDir("missing"))
	}
	// Success paths.
	stubDocker(t, 0)
	for _, name := range []string{"SaveApp", "SaveAppDraft", "StartApp", "RestartApp", "UpdateApp", "RenameApp", "StopApp", "DeleteApp"} {
		if err := mutators(r, "app-1")[name](context.Background()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		lockFree(t, r, "app-1", name)
	}
	if _, _, err := r.ReloadSharedVars(context.Background(), "missing"); err == nil {
		t.Fatal("reload of a missing app succeeded")
	}
	lockFree(t, r, "missing", "ReloadSharedVars")
}

func TestDeleteAppPrunesTheLockEntry(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.SaveAppDraft(context.Background(), draftPayload("app-1")); err != nil {
		t.Fatal(err)
	}
	stubDocker(t, 0)
	if _, err := r.DeleteApp(context.Background(), "app-1", false, false); err != nil {
		t.Fatal(err)
	}
	r.opMu.Lock()
	_, kept := r.opLocks["app-1"]
	r.opMu.Unlock()
	if kept {
		t.Fatal("DeleteApp kept the lock entry")
	}
}

func TestReloadWaitsForTheLockPastTheBusyDeadline(t *testing.T) {
	r := newTestRepo(t)
	r.busyWait = 10 * time.Millisecond
	unlock := r.lockAppOp("app-1")
	done := make(chan error, 1)
	go func() { _, _, err := r.ReloadSharedVars(context.Background(), "app-1"); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("reload did not wait for the lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	if err := <-done; err == nil || errors.Is(err, ErrAppBusy) {
		t.Fatalf("err = %v, want app not found", err)
	}
	// ... but it gives up when its ctx ends.
	unlock = r.lockAppOp("app-1")
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := r.ReloadSharedVars(ctx, "app-1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

// forgetApp prunes under sharedMu, so a RecomputeStale that is mid-run
// (holding sharedMu) cannot store a deleted app's entries after the prune.
func TestForgetAppWaitsForAStaleRecompute(t *testing.T) {
	r := newTestRepo(t)
	r.sharedMu.Lock() // a recompute in progress
	done := make(chan struct{})
	go func() { r.forgetApp("app-1"); close(done) }()
	select {
	case <-done:
		r.sharedMu.Unlock()
		t.Fatal("forgetApp did not wait for sharedMu")
	case <-time.After(50 * time.Millisecond):
	}
	// The recompute stores the app's entries, then finishes.
	r.refMu.Lock()
	r.refCache["app-1"] = refEntry{key: "k"}
	r.refMu.Unlock()
	r.setStale("app-1", []string{"SMTP_PASS"})
	r.sharedMu.Unlock()
	<-done
	r.refMu.Lock()
	_, cached := r.refCache["app-1"]
	r.refMu.Unlock()
	if cached || len(r.StaleApps()["app-1"]) != 0 {
		t.Fatal("a deleted app's shared-variable entries survived forgetApp")
	}
}

// A shared-variable reload starts containers, so it registers as an
// operation: it is refused while a restore of the app runs, and a restore
// does not begin while a reload holds the app.
func TestReloadAndRestoreExcludeEachOther(t *testing.T) {
	r := newTestRepo(t)
	end, err := r.beginRestore("app-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.ReloadSharedVars(context.Background(), "app-1"); !errors.Is(err, ErrRestoring) {
		t.Fatalf("reload during a restore: %v, want ErrRestoring", err)
	}
	lockFree(t, r, "app-1", "a reload refused by a restore")
	end()

	// The reload registers its operation once it holds the app's lock.
	if _, err := r.SaveAppDraft(context.Background(), draftPayload("app-1")); err != nil {
		t.Fatal(err)
	}
	unlock := r.lockAppOp("app-1")
	done := make(chan struct{})
	stubDocker(t, 0)
	go func() {
		defer close(done)
		_, _, _ = r.ReloadSharedVars(context.Background(), "app-1")
	}()
	// Held op lock: the reload waits and has not registered yet, so a
	// restore may begin (a restore never takes the operation lock).
	time.Sleep(20 * time.Millisecond)
	endRestore, err := r.beginRestore("app-1")
	if err != nil {
		t.Fatalf("restore refused while the reload only waits for the lock: %v", err)
	}
	unlock()
	<-done
	endRestore()
}

// A storage redeploy takes the app's operation lock, so it never runs
// beside a reload or a user action; a busy app is reported, not waited on
// past the busy deadline.
func TestRedeployLockedRespectsTheOperationLock(t *testing.T) {
	r := newTestRepo(t)
	r.busyWait = 10 * time.Millisecond
	calls := 0
	r.redeploy = func(context.Context, string) error { calls++; return nil }
	unlock := r.lockAppOp("app-1")
	if _, err := r.redeployLocked(context.Background(), "app-1"); !errors.Is(err, ErrAppBusy) {
		t.Fatalf("redeploy of a busy app: %v, want ErrAppBusy", err)
	}
	unlock()
	if skip, err := r.redeployLocked(context.Background(), "app-1"); err != nil || skip != redeployAllowed || calls != 1 {
		t.Fatalf("redeploy = %v, %v, calls %d", skip, err, calls)
	}
	lockFree(t, r, "app-1", "redeployLocked")
}

// storage.save checks for a draft before it waits for an app's lock; a
// draft saved while it waited (by the holder) must not be deployed over:
// the check is made again under the lock.
func TestStorageRedeploySkipsADraftSavedWhileItWaited(t *testing.T) {
	r := newTestRepo(t)
	stubDocker(t, 0)
	if _, err := r.SaveApp(context.Background(), draftPayload("app-1")); err != nil {
		t.Fatal(err)
	}
	r.runningProjects = func(context.Context) (map[string]bool, error) {
		return map[string]bool{projectName("app-1"): true}, nil
	}
	calls := 0
	r.redeploy = func(context.Context, string) error { calls++; return nil }

	unlock := r.lockAppOp("app-1") // a user's save is under way
	type result struct{ redeployed, warnings []string }
	done := make(chan result, 1)
	go func() {
		red, warn := r.redeployStorageUsers(context.Background(), []string{"app-1"})
		done <- result{red, warn}
	}()
	time.Sleep(20 * time.Millisecond) // the redeploy passed its first check and waits
	p := draftPayload("app-1")
	p.Files[0].Content = []byte("services: {web: {image: nginx}}\n")
	if _, _, err := r.save(p, false); err != nil { // the holder's draft commit
		t.Fatal(err)
	}
	unlock()
	res := <-done
	if calls != 0 || len(res.redeployed) != 0 {
		t.Fatalf("a draft was deployed over: calls %d, %+v", calls, res)
	}
	if len(res.warnings) != 1 || !strings.Contains(res.warnings[0], "undeployed changes") {
		t.Fatalf("warnings = %v", res.warnings)
	}
	// A restore begun meanwhile is reported the same way.
	end, err := r.beginRestore("app-1")
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	if skip, err := r.redeployLocked(context.Background(), "app-1"); err != nil || skip != redeployRestoring {
		t.Fatalf("redeploy during a restore = %v, %v", skip, err)
	}
}

// UpdateApp renders the env files before its pull, so an image tag driven
// by a shared variable is pulled with the current value.
func TestUpdateAppRendersEnvFilesBeforePull(t *testing.T) {
	r := newTestRepo(t)
	stubDocker(t, 0)
	if _, err := r.SaveApp(context.Background(), draftPayload("app-1")); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("app-1")
	if err := os.Remove(filepath.Join(dir, envSharedRel)); err != nil {
		t.Fatal(err)
	}
	var atPull []string
	r.runCompose = func(_ context.Context, d string, args []string) error {
		if args[len(args)-1] == "pull" {
			atPull = args
			if _, err := os.Stat(filepath.Join(d, envSharedRel)); err != nil {
				t.Errorf("pull ran before the shared file was rendered: %v", err)
			}
		}
		return nil
	}
	if err := r.UpdateApp(context.Background(), "app-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(atPull, " "), "--env-file "+envSharedRel) {
		t.Fatalf("pull args = %v", atPull)
	}
}
