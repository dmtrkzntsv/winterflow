package dockercompose

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/pkg/crypto/cryptotest"
)

// timeSleepShim keeps the polling test readable.
func timeSleepShim() { time.Sleep(100 * time.Millisecond) }

// sourcePayload builds a git-sourced app payload against a local upstream.
func sourcePayload(upstream string, composePath string) command.AppPayload {
	cfgSource := map[string]any{
		"repo_url":     upstream,
		"branch":       "master",
		"auto_update":  true,
		"poll_seconds": 1,
	}
	if composePath != "" {
		cfgSource["compose_path"] = composePath
	}
	cfg, _ := json.Marshal(map[string]any{
		"name":      "git app",
		"files":     []any{},
		"variables": []any{},
		"source":    cfgSource,
	})
	return command.AppPayload{
		AppID:  "git-app",
		Config: cfg,
		Source: &command.SourcePayload{
			RepoURL:     upstream,
			Branch:      "master",
			ComposePath: composePath,
			AutoUpdate:  true,
			PollSeconds: 1,
		},
	}
}

func TestSaveSourceAppPinsLockInCommit(t *testing.T) {
	r := newTestRepo(t)
	up, commit := newUpstream(t)
	c1 := commit("compose.yml", "services: {}\n")

	hash, err := r.saveWithoutDeploy(sourcePayload(up, ""))
	if err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("git-app")

	lock, ok := readSourceLock(dir)
	if !ok || lock.SHA != c1 {
		t.Fatalf("lock = %+v ok=%v want %s", lock, ok, c1)
	}
	// The lock must be part of the save commit: mutate it, restore the commit,
	// and expect the pinned value back.
	if err := writeSourceLock(dir, "0000000000000000000000000000000000000000"); err != nil {
		t.Fatal(err)
	}
	if err := gitRestore(dir, hash); err != nil {
		t.Fatal(err)
	}
	lock, _ = readSourceLock(dir)
	if lock.SHA != c1 {
		t.Fatalf("lock not committed with save: %+v", lock)
	}
	// source/ itself must NOT be tracked.
	if out, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); !strings.Contains(string(out), "source") {
		t.Fatalf(".gitignore misses source/: %q", out)
	}
}

func TestComposeFileResolution(t *testing.T) {
	r := newTestRepo(t)
	dir := filepath.Join(r.cfg.GetAppsDataDir(), "app-x")
	_ = os.MkdirAll(filepath.Join(dir, "source", "deploy"), 0o755)
	one := func(f string) []string { return []string{f} }

	// compose_path explicitly set.
	writeFileT(t, dir, "source/deploy/stack.yml", "x")
	if got := r.composeFiles(dir, &sourceSpec{ComposePath: "deploy/stack.yml"}); !reflect.DeepEqual(got, one(filepath.Join("source", "deploy", "stack.yml"))) {
		t.Fatalf("compose_path resolution = %q", got)
	}
	// Repo-root compose.yml auto-detected.
	writeFileT(t, dir, "source/compose.yml", "x")
	if got := r.composeFiles(dir, &sourceSpec{}); !reflect.DeepEqual(got, one(filepath.Join("source", "compose.yml"))) {
		t.Fatalf("root compose.yml resolution = %q", got)
	}
	// docker-compose.yml fallback.
	_ = os.Remove(filepath.Join(dir, "source", "compose.yml"))
	writeFileT(t, dir, "source/docker-compose.yml", "x")
	if got := r.composeFiles(dir, &sourceSpec{}); !reflect.DeepEqual(got, one(filepath.Join("source", "docker-compose.yml"))) {
		t.Fatalf("docker-compose.yml resolution = %q", got)
	}
	// Nothing in the repo: the winterflow-authored file in the app folder.
	_ = os.Remove(filepath.Join(dir, "source", "docker-compose.yml"))
	if got := r.composeFiles(dir, &sourceSpec{}); got != nil {
		t.Fatalf("no compose file anywhere = %q", got)
	}
	writeFileT(t, dir, "compose.yaml", "x")
	if got := r.composeFiles(dir, &sourceSpec{}); !reflect.DeepEqual(got, one("compose.yaml")) {
		t.Fatalf("fallback to the app folder = %q", got)
	}
	// An invalid compose_path falls back to the app folder too.
	if got := r.composeFiles(dir, &sourceSpec{ComposePath: "../etc/x.yml"}); !reflect.DeepEqual(got, one("compose.yaml")) {
		t.Fatalf("invalid compose_path = %q", got)
	}
	// Non-source apps name their file explicitly now, plus an override.
	writeFileT(t, dir, "compose.override.yml", "x")
	if got := r.composeFiles(dir, nil); !reflect.DeepEqual(got, []string{"compose.yaml", "compose.override.yml"}) {
		t.Fatalf("non-source app resolution = %q", got)
	}
	// A folder named like a compose file is not one.
	_ = os.Remove(filepath.Join(dir, "compose.yaml"))
	_ = os.MkdirAll(filepath.Join(dir, "compose.yaml"), 0o755)
	if got := r.composeFiles(dir, nil); got != nil {
		t.Fatalf("a folder counted as a compose file: %q", got)
	}
}

func TestComposeArgsAlwaysIncludeEnvFile(t *testing.T) {
	r := newTestRepo(t)
	savedApp(t, r, "app-1", "demo", "x\n")

	args := strings.Join(r.composeArgs("app-1", "up"), " ")
	if args != "compose --project-name wf-app-1 -f compose.yml --env-file .env up" {
		t.Fatalf("args = %s", args)
	}
	dir := r.appDataDir("app-1")
	writeFileT(t, dir, envSecretsRel, "")
	writeFileT(t, dir, storageEnvRel, "")
	args = strings.Join(r.composeArgs("app-1", "up"), " ")
	if !strings.HasSuffix(args, "-f compose.yml --env-file .env --env-file .winterflow/storage.env --env-file .env.secrets up") {
		t.Fatalf("env files in order: %s", args)
	}
}

func TestSourceTokenStoredEncryptedAndMasked(t *testing.T) {
	r, pub := newSecretRepo(t)
	up, commit := newUpstream(t)
	commit("compose.yml", "services: {}\n")

	p := sourcePayload(up, "")
	p.Source.Token = []byte(cryptotest.Encrypt(t, pub, "gh-token-plaintext"))
	if _, err := r.saveWithoutDeploy(p); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("git-app")

	raw, _ := os.ReadFile(filepath.Join(dir, secretsRel))
	if strings.Contains(string(raw), "gh-token-plaintext") {
		t.Fatal("token plaintext leaked into secrets.json")
	}
	var s secretStore
	_ = json.Unmarshal(raw, &s)
	if s.SourceToken == "" {
		t.Fatal("token ciphertext not stored")
	}
	if got := r.sourceTokenPlaintext(dir); got != "gh-token-plaintext" {
		t.Fatalf("decrypt round-trip = %q", got)
	}

	// GetApp masks the token.
	resp, err := r.GetApp(context.Background(), "git-app")
	if err != nil {
		t.Fatal(err)
	}
	if resp.App.Source == nil || string(resp.App.Source.Token) != command.EncryptedPlaceholder {
		t.Fatalf("GetApp source = %+v", resp.App.Source)
	}

	// Placeholder on re-save keeps the ciphertext.
	p2 := sourcePayload(up, "")
	p2.Source.Token = []byte(command.EncryptedPlaceholder)
	if _, err := r.saveWithoutDeploy(p2); err != nil {
		t.Fatal(err)
	}
	var s2 secretStore
	raw2, _ := os.ReadFile(filepath.Join(dir, secretsRel))
	_ = json.Unmarshal(raw2, &s2)
	if s2.SourceToken != s.SourceToken {
		t.Fatal("placeholder must preserve the token ciphertext")
	}
}

func TestRollbackRepinsSource(t *testing.T) {
	r := newTestRepo(t)
	up, commit := newUpstream(t)
	commit("marker.txt", "v1\n")

	h1, err := r.saveWithoutDeploy(sourcePayload(up, ""))
	if err != nil {
		t.Fatal(err)
	}

	// Upstream advances; a second save re-pins to the new head.
	commit("marker.txt", "v2\n")
	if _, err := r.saveWithoutDeploy(sourcePayload(up, "")); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("git-app")
	if got, _ := os.ReadFile(filepath.Join(dir, "source", "marker.txt")); string(got) != "v2\n" {
		t.Fatalf("pre-rollback source = %q", got)
	}

	// Rollback to the first save: the source checkout must return to v1.
	if _, err := r.rollbackWithoutDeploy("git-app", h1); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "source", "marker.txt")); string(got) != "v1\n" {
		t.Fatalf("post-rollback source = %q", got)
	}
}

func TestRefreshSourceWithoutDeploy(t *testing.T) {
	r := newTestRepo(t)
	up, commit := newUpstream(t)
	commit("f", "v1\n")
	if _, err := r.saveWithoutDeploy(sourcePayload(up, "")); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("git-app")
	before, _ := gitCount(dir)

	// No upstream change: no new commit.
	changed, _, err := r.refreshSourceWithoutDeploy("git-app")
	if err != nil || changed {
		t.Fatalf("no-change refresh: changed=%v err=%v", changed, err)
	}

	c2 := commit("f", "v2\n")
	changed, sha, err := r.refreshSourceWithoutDeploy("git-app")
	if err != nil || !changed || sha != c2 {
		t.Fatalf("refresh after advance: changed=%v sha=%s err=%v", changed, sha, err)
	}
	after, _ := gitCount(dir)
	if after != before+1 {
		t.Fatalf("refresh must commit the new pin: %d -> %d", before, after)
	}
}

func TestRefreshDueSourcesHonorsIntervalAndFlag(t *testing.T) {
	r := newTestRepo(t)
	up, commit := newUpstream(t)
	commit("f", "v1\n")

	// App A: auto_update on, 1s interval (test payload default).
	if _, err := r.saveWithoutDeploy(sourcePayload(up, "")); err != nil {
		t.Fatal(err)
	}
	// App B: auto_update off.
	pOff := sourcePayload(up, "")
	pOff.AppID = "git-app-off"
	cfg := map[string]any{}
	_ = json.Unmarshal(pOff.Config, &cfg)
	cfg["source"].(map[string]any)["auto_update"] = false
	pOff.Config, _ = json.Marshal(cfg)
	pOff.Source.AutoUpdate = false
	if _, err := r.saveWithoutDeploy(pOff); err != nil {
		t.Fatal(err)
	}

	// No upstream change: nothing due even after the interval.
	if ids := r.RefreshDueSources(context.Background()); len(ids) != 0 {
		t.Fatalf("no change -> no updates, got %v", ids)
	}

	commit("f", "v2\n")
	// First call after the save may be within the 1s window; wait it out.
	deadlineIDs := []string{}
	for i := 0; i < 30; i++ {
		deadlineIDs = r.RefreshDueSources(context.Background())
		if len(deadlineIDs) > 0 {
			break
		}
		timeSleepShim()
	}
	if len(deadlineIDs) != 1 || deadlineIDs[0] != "git-app" {
		t.Fatalf("want [git-app], got %v", deadlineIDs)
	}
	// The auto_update=false app must never be touched.
	for _, id := range deadlineIDs {
		if id == "git-app-off" {
			t.Fatal("auto_update=false app was refreshed")
		}
	}
	lock, _ := readSourceLock(r.appDataDir("git-app"))
	upHead, _ := gitLog(up)
	if lock.SHA != upHead[0].Hash {
		t.Fatalf("lock not advanced: %s vs %s", lock.SHA, upHead[0].Hash)
	}
}

// The upstream sync of a save (a clone or fetch that can take minutes) does
// not hold the app's lock: the other writers of the app's files (backup
// settings, a rename) are not kept waiting for it. A refused sync of a new
// app leaves no folder behind.
func TestSaveSyncsTheSourceWithoutHoldingTheAppLock(t *testing.T) {
	r := newTestRepo(t)
	entered, release := make(chan struct{}, 8), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		http.Error(w, "gone", http.StatusInternalServerError)
	}))
	defer srv.Close()
	var once sync.Once
	releaseAll := func() { once.Do(func() { close(release) }) }
	defer releaseAll()

	errc := make(chan error, 1)
	go func() {
		_, err := r.saveWithoutDeploy(sourcePayload(srv.URL+"/up.git", ""))
		errc <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the save never reached the upstream")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, err := r.lockApp(ctx, "git-app")
	if err != nil {
		t.Fatalf("the app's lock is held across the sync: %v", err)
	}
	unlock()
	// The source's own lock is.
	sctx, scancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer scancel()
	if _, err := r.lockSource(sctx, "git-app"); err == nil {
		t.Fatal("two operations on source/ at once")
	}
	releaseAll()
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "sync source") {
		t.Fatalf("save err = %v", err)
	}
	if _, err := os.Stat(r.appDataDir("git-app")); !os.IsNotExist(err) {
		t.Fatalf("a refused save of a new app left its folder: %v", err)
	}
}

// A source poll's commit stages everything, so it is made under the app's
// lock: with a writer between its files and its commit, the poll must not
// commit them under its own message.
func TestSourcePollCommitWaitsForTheAppLock(t *testing.T) {
	r := newTestRepo(t)
	up, commit := newUpstream(t)
	commit("compose.yml", "services: {}\n")
	if _, err := r.saveWithoutDeploy(sourcePayload(up, "")); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("git-app")
	c2 := commit("marker.txt", "v2\n")

	unlock, err := r.lockApp(context.Background(), "git-app")
	if err != nil {
		t.Fatal(err)
	}
	writeFileT(t, dir, "half-written.txt", "a writer's files\n") // not committed yet
	type result struct {
		changed bool
		err     error
	}
	done := make(chan result, 1)
	go func() {
		changed, _, err := r.refreshSourceWithoutDeploy("git-app")
		done <- result{changed, err}
	}()
	// The poll has synced the upstream (source.lock moved) and waits.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if lock, _ := readSourceLock(dir); lock.SHA == c2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the poll never synced the upstream")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case res := <-done:
		t.Fatalf("the poll committed while a writer held the app's lock: %+v", res)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := gitCommitAll(dir, "writer commit"); err != nil {
		t.Fatal(err)
	}
	unlock()
	select {
	case res := <-done:
		if res.err != nil || !res.changed {
			t.Fatalf("poll = %+v", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the poll never committed")
	}
	log, err := gitLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range log {
		if strings.HasPrefix(c.Subject, "update source") {
			t.Fatalf("the poll's commit took in the writer's files: %v", log)
		}
	}
	if log[0].Subject != "writer commit" {
		t.Fatalf("log = %v", log)
	}
}
