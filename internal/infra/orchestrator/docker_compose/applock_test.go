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
			defer r.lockApp("a")()
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
	unlockA := r.lockApp("a")
	done := make(chan struct{})
	go func() { r.lockApp("b")(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("app b waited for app a")
	}
	unlockA()
	r.forgetApp("a")
	r.lockMu.Lock()
	_, kept := r.appLocks["a"]
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
		"DeleteApp":    func(ctx context.Context) error { _, err := r.DeleteApp(ctx, appID, false); return err },
	}
}

func TestMutatorsGiveUpWhenAppBusy(t *testing.T) {
	r := newTestRepo(t)
	r.busyWait = 20 * time.Millisecond
	unlock := r.lockApp("app-1")
	defer unlock()
	for name, call := range mutators(r, "app-1") {
		start := time.Now()
		err := call(context.Background())
		if !errors.Is(err, ErrAppBusy) {
			t.Errorf("%s: err = %v, want ErrAppBusy", name, err)
			continue
		}
		if !strings.Contains(err.Error(), "app app-1 is busy (reloading shared variables), try again") {
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
	if _, err := r.ReloadSharedVars(context.Background(), "missing"); err == nil {
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
	if _, err := r.DeleteApp(context.Background(), "app-1", false); err != nil {
		t.Fatal(err)
	}
	r.lockMu.Lock()
	_, kept := r.appLocks["app-1"]
	r.lockMu.Unlock()
	if kept {
		t.Fatal("DeleteApp kept the lock entry")
	}
}

func TestReloadWaitsForTheLockPastTheBusyDeadline(t *testing.T) {
	r := newTestRepo(t)
	r.busyWait = 10 * time.Millisecond
	unlock := r.lockApp("app-1")
	done := make(chan error, 1)
	go func() { _, err := r.ReloadSharedVars(context.Background(), "app-1"); done <- err }()
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
	unlock = r.lockApp("app-1")
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := r.ReloadSharedVars(ctx, "app-1"); !errors.Is(err, context.DeadlineExceeded) {
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
