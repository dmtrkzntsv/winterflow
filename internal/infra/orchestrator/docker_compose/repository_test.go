package dockercompose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
)

// savedApp writes an app via the deploy-free save path and returns its hash.
func savedApp(t *testing.T, r *Repository, appID, name, compose string) string {
	t.Helper()
	hash, err := r.saveWithoutDeploy(command.AppPayload{
		AppID:  appID,
		Config: []byte(`{"name":"` + name + `","files":[{"filename":"compose.yml","is_encrypted":false}],"variables":[{"name":"PORT","is_encrypted":false}]}`),
		Files: []command.ContentItem{
			{Name: "compose.yml", Content: []byte(compose)},
		},
		Variables: []command.ContentItem{
			{Name: "PORT", Content: []byte("8080")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestSaveCreatesCommitAndSymlink(t *testing.T) {
	r := newTestRepo(t)
	hash := savedApp(t, r, "app-1", "grafana", "services: {}\n")
	if hash == "" {
		t.Fatal("no hash returned")
	}

	dir := r.appDataDir("app-1")
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatal("app dir is not a git repo")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "compose.yml")); string(got) != "services: {}\n" {
		t.Fatalf("compose.yml = %q", got)
	}
	if n, _ := gitCount(dir); n != 1 {
		t.Fatalf("commit count = %d", n)
	}
	// The human-readable symlink resolves to the data dir.
	target, err := os.Readlink(filepath.Join(r.cfg.GetAppsDir(), "grafana"))
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join("..", "apps-data", "app-1") {
		t.Fatalf("symlink target = %q", target)
	}
}

func TestSecondSaveGrowsHistory(t *testing.T) {
	r := newTestRepo(t)
	h1 := savedApp(t, r, "app-1", "grafana", "one\n")
	h2 := savedApp(t, r, "app-1", "grafana", "two\n")
	if h1 == h2 {
		t.Fatal("expected a new commit for the second save")
	}
	if n, _ := gitCount(r.appDataDir("app-1")); n != 2 {
		t.Fatalf("count = %d", n)
	}
}

func TestSaveAppRequiresAppID(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.saveWithoutDeploy(command.AppPayload{}); err == nil {
		t.Fatal("expected app_id validation error")
	}
}

func TestListAppsReadsNewLayoutAndHealsSymlinks(t *testing.T) {
	r := newTestRepo(t)
	savedApp(t, r, "app-1", "grafana", "x\n")
	savedApp(t, r, "app-2", "postgres", "y\n")

	// Sabotage: remove one link, add a dangling one.
	if err := os.Remove(filepath.Join(r.cfg.GetAppsDir(), "grafana")); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(filepath.Join("..", "apps-data", "ghost"), filepath.Join(r.cfg.GetAppsDir(), "zombie"))

	apps, err := r.ListApps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 {
		t.Fatalf("apps = %+v", apps)
	}
	byID := map[string]string{}
	versions := map[string]string{}
	for _, a := range apps {
		byID[a.ID] = a.Name
		versions[a.ID] = a.Version
	}
	if byID["app-1"] != "grafana" || byID["app-2"] != "postgres" {
		t.Fatalf("names = %v", byID)
	}
	if versions["app-1"] != "1" {
		t.Fatalf("version should be the commit count, got %q", versions["app-1"])
	}
	// Healed: grafana link is back, zombie is gone.
	if _, err := os.Readlink(filepath.Join(r.cfg.GetAppsDir(), "grafana")); err != nil {
		t.Fatal("missing link not healed")
	}
	if _, err := os.Lstat(filepath.Join(r.cfg.GetAppsDir(), "zombie")); !os.IsNotExist(err) {
		t.Fatal("dangling link not pruned")
	}
}

func TestListAppsEmptyRootAndErrorPropagation(t *testing.T) {
	r := newTestRepo(t)
	apps, err := r.ListApps(context.Background())
	if err != nil || len(apps) != 0 {
		t.Fatalf("empty root: %v %v", apps, err)
	}
	statuses, err := r.GetAppsStatus(context.Background())
	if err != nil || len(statuses) != 0 {
		t.Fatalf("empty root status: %v %v", statuses, err)
	}

	// A file where the dir should be → real error.
	if err := os.WriteFile(r.cfg.GetAppsDataDir(), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ListApps(context.Background()); err == nil {
		t.Fatal("expected error for blocked apps-data root")
	}
	if _, err := r.GetAppsStatus(context.Background()); err == nil {
		t.Fatal("expected error for blocked apps-data root")
	}
}

func TestGetAppRoundTripAndMasking(t *testing.T) {
	r, pub := newSecretRepo(t)
	p := storePayload(pub, t) // has secret var DB_PASS + secret file certs/tls.key
	if _, err := r.saveWithoutDeploy(p); err != nil {
		t.Fatal(err)
	}

	resp, err := r.GetApp(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.App.Config) == "" {
		t.Fatal("config missing")
	}

	items := map[string]command.ContentItem{}
	for _, v := range resp.App.Variables {
		items["var:"+v.Name] = v
	}
	for _, f := range resp.App.Files {
		items["file:"+f.Name] = f
	}
	if string(items["var:PORT"].Content) != "8080" {
		t.Fatalf("PORT = %q", items["var:PORT"].Content)
	}
	if v := items["var:DB_PASS"]; !v.Encrypted || string(v.Content) != command.EncryptedPlaceholder {
		t.Fatalf("DB_PASS not masked: %+v", v)
	}
	if f := items["file:compose.yml"]; string(f.Content) != "services: {}\n" {
		t.Fatalf("compose.yml = %q", f.Content)
	}
	if f := items["file:certs/tls.key"]; !f.Encrypted || string(f.Content) != command.EncryptedPlaceholder {
		t.Fatalf("secret file not masked: %+v", f)
	}
}

func TestGetAppMissing(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.GetApp(context.Background(), "nope"); err == nil {
		t.Fatal("expected error for unknown app")
	}
}

func TestRenameAppCommitsAndSwapsSymlink(t *testing.T) {
	r := newTestRepo(t)
	savedApp(t, r, "app-1", "oldname", "x\n")

	if err := r.RenameApp(context.Background(), "app-1", "newname"); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("app-1")
	cfg, _, _ := r.readAppConfig(dir)
	if cfg["name"] != "newname" {
		t.Fatalf("config name = %v", cfg["name"])
	}
	if n, _ := gitCount(dir); n != 2 {
		t.Fatalf("rename should commit; count = %d", n)
	}
	if _, err := os.Lstat(filepath.Join(r.cfg.GetAppsDir(), "oldname")); !os.IsNotExist(err) {
		t.Fatal("old symlink should be gone")
	}
	if _, err := os.Readlink(filepath.Join(r.cfg.GetAppsDir(), "newname")); err != nil {
		t.Fatal("new symlink missing")
	}
}

func TestRenameAppMissingConfig(t *testing.T) {
	r := newTestRepo(t)
	if err := r.RenameApp(context.Background(), "nope", "x"); err == nil {
		t.Fatal("expected error")
	}
}

func TestRevisionsNewestFirst(t *testing.T) {
	r := newTestRepo(t)
	h1 := savedApp(t, r, "app-1", "demo", "one\n")
	h2 := savedApp(t, r, "app-1", "demo", "two\n")

	revs, current, err := r.Revisions(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if current != h2 {
		t.Fatalf("current = %s, want %s", current, h2)
	}
	if len(revs) != 2 || revs[0].Hash != h2 || revs[1].Hash != h1 {
		t.Fatalf("revs = %+v", revs)
	}
	if !strings.HasPrefix(revs[0].Subject, "save ") || revs[0].Timestamp == 0 {
		t.Fatalf("rev meta = %+v", revs[0])
	}
}

func TestRevisionsUnknownApp(t *testing.T) {
	r := newTestRepo(t)
	if _, _, err := r.Revisions(context.Background(), "nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestRollbackRestoresTreeAsNewCommit(t *testing.T) {
	r := newTestRepo(t)
	h1 := savedApp(t, r, "app-1", "demo", "version-one\n")
	savedApp(t, r, "app-1", "demo", "version-two\n")

	newHead, err := r.rollbackWithoutDeploy("app-1", h1)
	if err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("app-1")
	if got, _ := os.ReadFile(filepath.Join(dir, "compose.yml")); string(got) != "version-one\n" {
		t.Fatalf("compose.yml = %q", got)
	}
	if n, _ := gitCount(dir); n != 3 {
		t.Fatalf("rollback must add a commit; count = %d", n)
	}
	if newHead == h1 {
		t.Fatal("rollback head must be new")
	}
	revs, current, _ := r.Revisions(context.Background(), "app-1")
	if current != newHead || !strings.HasPrefix(revs[0].Subject, "rollback to ") {
		t.Fatalf("history after rollback: %+v", revs)
	}
}

func TestRollbackUnknownAppOrHash(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.rollbackWithoutDeploy("nope", "abc"); err == nil {
		t.Fatal("unknown app must error")
	}
	savedApp(t, r, "app-1", "demo", "x\n")
	if _, err := r.rollbackWithoutDeploy("app-1", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"); err == nil {
		t.Fatal("unknown hash must error")
	}
}

func TestDeleteAppRemovesDirAndSymlink(t *testing.T) {
	r := newTestRepo(t)
	savedApp(t, r, "app-1", "demo", "x\n")

	if err := r.DeleteApp(context.Background(), "app-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.appDataDir("app-1")); !os.IsNotExist(err) {
		t.Fatal("app dir should be removed")
	}
	if _, err := os.Lstat(filepath.Join(r.cfg.GetAppsDir(), "demo")); !os.IsNotExist(err) {
		t.Fatal("symlink should be removed")
	}
}

func TestStopAppMissingIsNoop(t *testing.T) {
	r := newTestRepo(t)
	if err := r.StopApp(context.Background(), "missing"); err != nil {
		t.Fatalf("stop on missing app must be a no-op: %v", err)
	}
}

func TestControlActionsOnMissingAppFail(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if err := r.StartApp(ctx, "missing"); err == nil {
		t.Fatal("start should fail")
	}
	if err := r.RestartApp(ctx, "missing"); err == nil {
		t.Fatal("restart should fail")
	}
	if err := r.UpdateApp(ctx, "missing"); err == nil {
		t.Fatal("update should fail")
	}
}

func TestComposeArgsIncludeEnvFilesOnlyWithSecrets(t *testing.T) {
	r := newTestRepo(t)
	savedApp(t, r, "app-1", "demo", "x\n")

	args := strings.Join(r.composeArgs("app-1", "up"), " ")
	if strings.Contains(args, "--env-file") {
		t.Fatalf("no secrets -> no explicit env files: %s", args)
	}

	// Materialized secrets flip the flags on.
	dir := r.appDataDir("app-1")
	if err := os.WriteFile(filepath.Join(dir, envSecretsRel), []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args = strings.Join(r.composeArgs("app-1", "up"), " ")
	if !strings.Contains(args, "--env-file .env --env-file .env.secrets") {
		t.Fatalf("secrets present -> both env files expected: %s", args)
	}
}

func TestProjectName(t *testing.T) {
	if projectName("abc") != "wf-abc" {
		t.Fatal(projectName("abc"))
	}
	if projectName("../../etc") != "wf-etc" {
		t.Fatalf("traversal not stripped: %s", projectName("../../etc"))
	}
}

func TestGetLogsNotDeployedIsEmpty(t *testing.T) {
	r := newTestRepo(t)
	resp, err := r.GetLogs(context.Background(), command.GetLogsRequest{AppID: "missing", Tail: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Logs) != 0 {
		t.Fatalf("logs = %+v", resp.Logs)
	}
}

func TestSaveConfigRoundTripThroughJSON(t *testing.T) {
	// The committed config.json is the API-authored blob; model.App parses its
	// name/icon/color fields for the reconcile list.
	r := newTestRepo(t)
	savedApp(t, r, "app-1", "demo", "x\n")
	_, raw, err := r.readAppConfig(r.appDataDir("app-1"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m["name"] != "demo" {
		t.Fatalf("config = %s (%v)", raw, err)
	}
}
