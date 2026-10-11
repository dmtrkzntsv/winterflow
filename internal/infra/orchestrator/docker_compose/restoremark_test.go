package dockercompose

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/helper"
)

// readMarks is restoring.json as it is on disk now (nil: no file).
func readMarks(t *testing.T, r *Repository) (list []RestoreMark, mode os.FileMode) {
	t.Helper()
	raw, err := os.ReadFile(r.restoreMarkPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("restoring.json: %v\n%s", err, raw)
	}
	fi, _ := os.Stat(r.restoreMarkPath())
	return list, fi.Mode().Perm()
}

// While a restore runs, restoring.json says where it is (0600); it is gone
// once the restore has ended.
func TestRestoreRecordsItselfWhileItRuns(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	type seen struct {
		marks []RestoreMark
		mode  os.FileMode
	}
	var atStop, atHelper seen
	rr.runCompose = func(_ context.Context, _ string, args []string) error {
		rr.compose = append(rr.compose, composeVerb(args))
		if composeVerb(args) == "stop" {
			atStop.marks, atStop.mode = readMarks(t, rr.Repository)
		}
		return nil
	}
	rr.restorer.onRun = func() { atHelper.marks, atHelper.mode = readMarks(t, rr.Repository) }
	if _, err := rr.restore(context.Background(), "database", id); err != nil {
		t.Fatal(err)
	}
	if len(atStop.marks) != 1 || atStop.marks[0].Phase != "stopping" || !atStop.marks[0].WasRunning ||
		atStop.marks[0].App != "app-1" || atStop.marks[0].Backup != "database" || atStop.marks[0].Point != id || atStop.mode != 0o600 {
		t.Fatalf("when the app was stopped: %+v (%v)", atStop.marks, atStop.mode)
	}
	if len(atHelper.marks) != 1 || atHelper.marks[0].Phase != "restoring" || atHelper.marks[0].PreRestorePoint != "20261011T130000Z-pre-restore" ||
		atHelper.marks[0].Started.IsZero() || atHelper.mode != 0o600 {
		t.Fatalf("while the helper ran: %+v (%v)", atHelper.marks, atHelper.mode)
	}
	if marks, _ := readMarks(t, rr.Repository); marks != nil {
		t.Fatalf("after the restore: %+v", marks)
	}
}

// Every way a restore ends removes its marker: the error it returns is what
// tells the user then.
func TestRestoreMarkerGoneAfterFailures(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	rr.restorer.exit, rr.restorer.stderr = helper.ExitPartial, `{"error":"half-way"}`+"\n"
	if _, err := rr.restore(context.Background(), "database", id); err == nil {
		t.Fatal("expected a failure")
	}
	if marks, _ := readMarks(t, rr.Repository); marks != nil {
		t.Fatalf("after a half-done swap: %+v", marks)
	}
	rr.up = true
	rr.restorer.exit, rr.restorer.stderr = helper.ExitFailed, `{"error":"no"}`+"\n"
	if _, err := rr.restore(context.Background(), "database", id); err == nil {
		t.Fatal("expected a failure")
	}
	if marks, _ := readMarks(t, rr.Repository); marks != nil {
		t.Fatalf("after a clean failure: %+v", marks)
	}
}

// What an agent that stopped mid-restore left is read once at the next start,
// removed, and names the app and the pre-restore copy; nothing is started.
func TestInterruptedRestoresAreReadOnceAndRemoved(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	var left []RestoreMark
	rr.restorer.onRun = func() { left, _ = readMarks(t, rr.Repository) } // the file as a crash would leave it
	if _, err := rr.restore(context.Background(), "database", id); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(append(left, RestoreMark{App: "deleted-app", Backup: "db", Point: "x", Phase: "restoring"}))
	if err := os.WriteFile(rr.restoreMarkPath(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	rr.compose = nil
	got := rr.InterruptedRestores()
	if len(got) != 1 || got[0].App != "app-1" {
		t.Fatalf("interrupted = %+v", got)
	}
	want := "the agent stopped during a restore of " + id + " (pre-restore copy 20261011T130000Z-pre-restore); check the app's data: it may be partly replaced, and the app may have been left stopped"
	if got[0].Message() != want {
		t.Fatalf("message = %q", got[0].Message())
	}
	if _, err := os.Stat(rr.restoreMarkPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restoring.json is still there: %v", err)
	}
	if again := rr.InterruptedRestores(); again != nil {
		t.Fatalf("second read = %+v", again)
	}
	if len(rr.compose) != 0 {
		t.Fatalf("the app was touched: %v", rr.compose)
	}
	if m := (RestoreMark{Point: "p", Phase: "stopping"}).Message(); m != "the agent stopped during a restore of p (no pre-restore copy had been saved yet); check the app's data" {
		t.Fatalf("message = %q", m)
	}
}

// An unreadable file is dropped, not retried at every start.
func TestInterruptedRestoresDropsAGarbledFile(t *testing.T) {
	rr := newRestoreRepo(t)
	if err := os.MkdirAll(strings.TrimSuffix(rr.restoreMarkPath(), "/"+restoreMarkName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rr.restoreMarkPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := rr.InterruptedRestores(); got != nil {
		t.Fatalf("interrupted = %+v", got)
	}
	if _, err := os.Stat(rr.restoreMarkPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restoring.json is still there: %v", err)
	}
}

// The agent does not update itself (and so exit) while a restore runs; once
// it has none, the update goes on to download. After an update is under way
// no restore begins.
func TestAgentUpdateIsRefusedWhileRestoring(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("GITHUB_RELEASES_URL", srv.URL)
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	var during error
	rr.restorer.onRun = func() {
		_, during = rr.UpdateAgent(context.Background(), updateTo("999.0.0"))
	}
	if _, err := rr.restore(context.Background(), "database", id); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(during, ErrRestoreRunning) || !strings.Contains(during.Error(), "not updated while a backup is being restored") || hits != 0 {
		t.Fatalf("update during a restore: %v (%d downloads)", during, hits)
	}
	// Without a restore it reaches the download (which 404s here).
	if _, err := rr.UpdateAgent(context.Background(), updateTo("999.0.0")); err == nil || errors.Is(err, ErrRestoreRunning) || hits != 1 {
		t.Fatalf("update without a restore: %v (%d downloads)", err, hits)
	}
	// ...and a failed update lets restores begin again.
	if _, err := rr.restore(context.Background(), "database", id); err != nil {
		t.Fatalf("restore after a failed update: %v", err)
	}
}

func TestNoRestoreBeginsOnceTheAgentIsUpdating(t *testing.T) {
	r := newTestRepo(t)
	undo, err := r.beginUpdate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.beginRestore("app-1"); !errors.Is(err, ErrAgentUpdating) {
		t.Fatalf("restore during an update: %v", err)
	}
	undo()
	end, err := r.beginRestore("app-1")
	if err != nil {
		t.Fatalf("restore after the update failed: %v", err)
	}
	if _, err := r.beginUpdate(); !errors.Is(err, ErrRestoreRunning) {
		t.Fatalf("update during a restore: %v", err)
	}
	end()
}

func updateTo(v string) command.UpdateAgentRequest { return command.UpdateAgentRequest{Version: v} }
